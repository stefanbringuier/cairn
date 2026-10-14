"""Check profile isolation and legacy compatibility using disposable repositories."""

import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys
import tempfile


BINARY = str(Path(sys.argv[1]).resolve())


def main():
    with tempfile.TemporaryDirectory(prefix="cairn-profiles-") as directory:
        root = Path(directory)
        workspace = root / "home/projects"
        cloud = workspace / "cloud"
        cloud.mkdir(parents=True)
        tools = root / "tools"
        tools.mkdir()
        config = root / "config/cairn"
        config.mkdir(parents=True)
        units = root / "config/systemd/user"
        units.mkdir(parents=True)
        enabled = root / "runtime/enabled"
        enabled.mkdir(parents=True)
        tools.joinpath("loginctl").write_text("#!/bin/sh\nexit 0\n")
        tools.joinpath("loginctl").chmod(0o700)
        # Manifest collection uses deterministic local fixtures, never host package daemons.
        for name in ("apt-mark", "snap", "flatpak", "brew", "uv", "pipx", "cargo", "npm", "crontab"):
            contents = "#!/bin/sh\nprintf 'fixture-package\\n'\n" if name == "apt-mark" else "#!/bin/sh\nexit 1\n"
            tools.joinpath(name).write_text(contents)
            tools.joinpath(name).chmod(0o700)
        tools.joinpath("systemctl").write_text('''#!/bin/sh
printf '%s\n' "$*" >> "$CAIRN_PROFILE_LOG"
shift
operation=$1
shift
case "$operation" in
  enable) for unit in "$@"; do case "$unit" in *.timer) touch "$CAIRN_PROFILE_ENABLED/$unit" ;; esac; done ;;
  disable) for unit in "$@"; do case "$unit" in *.timer) rm -f "$CAIRN_PROFILE_ENABLED/$unit" ;; esac; done ;;
  is-enabled) if [ -f "$CAIRN_PROFILE_ENABLED/$1" ]; then echo enabled; else echo disabled; exit 1; fi ;;
  show) printf 'NextElapseUSecRealtime=\nActiveState=active\n\nActiveState=inactive\nResult=success\nExecMainStatus=0\nExecMainExitTimestamp=\n' ;;
esac
exit 0
''')
        tools.joinpath("systemctl").chmod(0o700)
        env = {key: value for key, value in os.environ.items()
               if not key.startswith(("RESTIC_", "RCLONE_"))}
        env.update({
            "HOME": str(root / "home"), "PATH": str(tools) + os.pathsep + os.environ["PATH"],
            "XDG_CONFIG_HOME": str(root / "config"), "XDG_STATE_HOME": str(root / "state"),
            "XDG_DATA_HOME": str(root / "data"), "XDG_CACHE_HOME": str(root / "cache"),
            "XDG_RUNTIME_DIR": str(root / "runtime"), "TMPDIR": str(root / "tmp"),
            "CAIRN_PROFILE_LOG": str(root / "systemctl.log"), "CAIRN_PROFILE_ENABLED": str(enabled),
        })
        Path(env["TMPDIR"]).mkdir()

        def run(*args, expected=0):
            result = subprocess.run([BINARY, *args], env=env, cwd=workspace,
                                    stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=120)
            assert result.returncode == expected, (args, result.returncode, result.stdout, result.stderr)
            return result.stdout + result.stderr

        def restic(repo, password, *args):
            return subprocess.run([shutil.which("restic"), "--repo", str(repo), "--password-file", str(password), *args],
                                  env=env, check=True, capture_output=True, text=True, timeout=120).stdout

        # A pre-feature installation, with unchanged legacy settings/password/unit files.
        legacy_repo = workspace / "nas-repository"
        cloud_repo = workspace / "cloud-repository"
        legacy_password = config / "password"
        legacy_password.write_text("disposable legacy fixture password\n")
        legacy_password.chmod(0o600)
        legacy_config = {
            "target": str(legacy_repo), "repo": str(legacy_repo), "host": "profile-fixture",
            "snapshot": {"every": "1h", "keep": "100,24h", "paths": [str(workspace)]},
            "backup": {"every": "daily", "keep": "100,7d", "paths": [str(workspace)]},
            "exclude": ["*.iso"],
        }
        config.joinpath("config.json").write_text(json.dumps(legacy_config))
        old_units = {}
        for kind in ("snapshot", "backup"):
            for extension in ("service", "timer"):
                path = units / f"cairn-{kind}.{extension}"
                path.write_text(f"[Unit]\nDescription=legacy {kind} {extension}\n")
                old_units[path] = path.read_bytes()
            enabled.joinpath(f"cairn-{kind}.timer").touch()
        old_config = config.joinpath("config.json").read_bytes()
        old_password = legacy_password.read_bytes()
        notes = workspace / "notes.txt"
        notes.write_text("legacy saved notes\n")
        cloud_notes = cloud / "notes.txt"
        cloud_notes.write_text("cloud saved notes\n")
        restic(legacy_repo, legacy_password, "init")
        run("--profile", "cloud", "init", str(cloud_repo), "--password", "--path", str(cloud), "-y")
        cloud_password = config / "profiles/cloud/password"
        assert cloud_password.exists() and cloud_password.read_bytes() != old_password
        assert config.joinpath("config.json").read_bytes() == old_config
        assert legacy_password.read_bytes() == old_password
        assert all(path.read_bytes() == contents for path, contents in old_units.items())
        assert not config.joinpath("profiles/default").exists(), "Legacy files must remain in place"
        run("--profile", "cloud", "set", "snapshot.keep", "100,24h")
        run("--profile", "cloud", "set", "backup.every", "off")
        run("--profile", "cloud", "set", "backup.paths", str(cloud))
        assert config.joinpath("config.json").read_bytes() == old_config
        inventory = json.loads(run("profiles", "--json"))
        assert [item["name"] for item in inventory] == ["cloud", "default"]
        assert inventory[0]["snapshot"]["enabled"] is True
        assert inventory[0]["backup"]["enabled"] is False
        assert inventory[1]["snapshot"]["timer"] == "cairn-snapshot.timer"
        assert inventory[1]["backup"]["enabled"] is True
        print("Passed: legacy config/password/units preserved; named settings, credentials and timer enabled states isolated.", flush=True)

        service = units.joinpath("cairn-cloud-snapshot.service").read_text()
        assert '--profile "cloud" snapshot' in service
        for key in ("XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"):
            assert f'Environment="{key}={env[key]}"' in service
        # Run the actual generated command using only its generated environment roots.
        timer_env = {"HOME": env["HOME"], "CAIRN_PROFILE_LOG": env["CAIRN_PROFILE_LOG"],
                     "CAIRN_PROFILE_ENABLED": env["CAIRN_PROFILE_ENABLED"]}
        for line in service.splitlines():
            if line.startswith("Environment="):
                key, value = shlex.split(line.removeprefix("Environment="))[0].split("=", 1)
                timer_env[key] = value
        command = shlex.split(next(line.removeprefix("ExecStart=") for line in service.splitlines()
                                  if line.startswith("ExecStart=")))
        subprocess.run(command, env=timer_env, cwd=root, check=True, capture_output=True, text=True, timeout=120)
        run("snapshot")
        legacy_id = json.loads(run("history", "notes.txt", "--json"))[0]["snapshot"]
        cloud_id = json.loads(run("--profile=cloud", "history", str(cloud_notes), "--json"))[0]["snapshot"]
        assert cloud_id != legacy_id
        assert json.loads(run("status", "--json"))["profile"] == "default"
        assert json.loads(run("--profile", "cloud", "status", "--json"))["profile"] == "cloud"
        assert json.loads(run("--profile", "cloud", "history", str(notes), "--json")) == []
        run("--profile", "cloud", "recover", str(notes), "--at", legacy_id, expected=1)
        listed = restic(legacy_repo, legacy_password, "ls", "--json", legacy_id)
        for repo in (legacy_repo, cloud_repo):
            assert not any(json.loads(line).get("path", "").startswith(str(repo) + "/") for line in listed.splitlines())
        assert root.joinpath("state/cairn/pruned").exists()
        assert root.joinpath("state/cairn/profiles/cloud/pruned").exists()

        cloud_notes.write_text("unsaved cloud work\n")
        recovered = json.loads(run("--profile", "cloud", "recover", str(cloud_notes), "--at", cloud_id, "--json"))
        assert Path(recovered["destination"]).read_text() == "cloud saved notes\n"
        assert cloud_notes.read_text() == "unsaved cloud work\n"
        run("--profile", "cloud", "recover", str(cloud_notes), "--to", str(cloud_notes), expected=1)
        run("--profile", "cloud", "restore", str(cloud_notes), expected=2)
        output = run("--profile", "cloud", "restore", str(cloud_notes), "--at", cloud_id, "-y")
        undo = re.search(r"cairn --profile cloud restore --at ([0-9a-f]{64}) -y --", output)
        assert undo and cloud_notes.read_text() == "cloud saved notes\n"
        run("--profile", "cloud", "restore", str(cloud_notes), "--at", undo[1], "-y")
        assert cloud_notes.read_text() == "unsaved cloud work\n"
        assert notes.read_text() == "legacy saved notes\n"
        print("Passed: generated timer roots/profile, separate snapshots/pruning state, profile-qualified recover/restore/undo and unchanged legacy source.", flush=True)
        comparison = json.loads(run("--profile", "cloud", "diff", cloud_id, undo[1], "--path", str(cloud_notes), "--json"))
        assert comparison["summary"]["modified"] == 1
        for forbidden in (legacy_password, config / "profiles/new/password", legacy_repo / "new-output"):
            run("--profile", "cloud", "restore", str(cloud_notes), "--at", cloud_id, "--to", str(forbidden), "-y", expected=1)
        assert legacy_password.read_bytes() == old_password
        run("--profile", "cloud", "backup")
        assert root.joinpath("state/cairn/profiles/cloud/manifests").is_dir()
        assert root.joinpath("state/cairn/profiles/cloud/manifests/apt.txt").read_text() == "fixture-package\n"
        assert not root.joinpath("state/cairn/manifests").exists()

        # Legacy credentials and profile configs may be linked outside the config root.
        secrets = root / "cloud"
        secrets.mkdir()
        external_password = secrets / "password"
        legacy_password.rename(external_password)
        legacy_password.symlink_to(external_password)
        external_config = secrets / "config.json"
        cloud_config = config / "profiles/cloud/config.json"
        cloud_config.rename(external_config)
        cloud_config.symlink_to(external_config)
        for protected in (external_password, external_config):
            run("--profile", "cloud", "restore", str(cloud_notes), "--at", cloud_id, "--to", str(protected), "-y", expected=1)
        run("--profile", "cloud", "restore", str(cloud), "--at", cloud_id, "--to", str(root), "-y", expected=1)
        assert external_password.read_bytes() == old_password
        safe_copy = root / "linked-config-recovery"
        run("--profile", "cloud", "recover", str(cloud_notes), "--at", cloud_id, "--to", str(safe_copy))
        assert safe_copy.read_text() == "cloud saved notes\n"

        alias = workspace / "nas-alias"
        alias.symlink_to(legacy_repo)
        run("--profile", "duplicate", "init", str(alias), "-y", expected=1)
        assert not config.joinpath("profiles/duplicate/config.json").exists()
        before_cloud = config.joinpath("profiles/cloud/config.json").read_bytes()
        run("--profile", "cloud", "init", "rest:https://127.0.0.1:1/unreachable", expected=2)
        assert config.joinpath("profiles/cloud/config.json").read_bytes() == before_cloud
        failed_repo = root / "unconfigured-encrypted-repository"
        restic(failed_repo, legacy_password, "init")
        run("--profile", "failed", "init", str(failed_repo), "--password", "-y", expected=1)
        assert not config.joinpath("profiles/failed/password").exists()
        assert not config.joinpath("profiles/failed/config.json").exists()
        run("--profile", "../escape", "init", str(root / "escape-repo"), "-y", expected=2)
        assert not root.joinpath("escape-repo").exists()
        # Reinitializing the default on the same repository preserves all settings and credentials.
        run("init", str(legacy_repo), "-y")
        current_legacy = json.loads(config.joinpath("config.json").read_text())
        for key in ("snapshot", "backup", "exclude"):
            assert current_legacy[key] == legacy_config[key]
        assert legacy_password.read_bytes() == old_password
        # A data-only HOME source must not collect recipes from other profiles or orphaned state.
        nested_state = root / "home/.local/state"
        env["XDG_STATE_HOME"] = str(nested_state)
        for relative in ("cairn/manifests", "cairn/profiles/cloud/manifests", "cairn/profiles/orphan/manifests"):
            recipe = nested_state / relative
            recipe.mkdir(parents=True)
            recipe.joinpath("apt.txt").write_text("stale-machine-package\n")
        data_repo = root / "data-only-repository"
        run("--profile", "data", "init", str(data_repo), "--data-only", "--path", env["HOME"], "--password", "-y")
        run("--profile", "data", "set", "snapshot.keep", "100,24h")
        run("--profile", "data", "snapshot")
        run("--profile", "data", "backup")
        data_password = config / "profiles/data/password"
        for snapshot in json.loads(restic(data_repo, data_password, "snapshots", "--json")):
            listing = restic(data_repo, data_password, "ls", "--json", snapshot["id"])
            paths = [json.loads(line).get("path", "") for line in listing.splitlines()]
            assert not any("/cairn/manifests" in path or re.search(r"/cairn/profiles/[^/]+/manifests", path) for path in paths), paths
            if "backup" in snapshot["tags"]:
                assert "data-only" in snapshot["tags"]
        assert not nested_state.joinpath("cairn/profiles/data/manifests").exists()
        assert json.loads(run("--profile", "data", "status", "--json"))["backup_scope"] == "data-only"
        scopes = {item["name"]: item["backup_scope"] for item in json.loads(run("profiles", "--json"))}
        assert scopes == {"cloud": "machine", "data": "data-only", "default": "machine"}
        print("Passed: legacy profiles in place, isolated encrypted repositories/settings/state, independent timer inventory, generated timer XDG/profile invocation, exclusions, profile-qualified diff/recover/restore/undo, external config/password link protection, rejected duplicate repositories and failed init rollback.")


if __name__ == "__main__":
    main()
