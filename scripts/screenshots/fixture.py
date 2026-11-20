"""Run the real TUI in a PTY backed by disposable files and a restic repo."""

import base64
import datetime
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import select
import signal
import shlex
import shutil
import struct
import subprocess
import sys
import tempfile
import termios
import time

# Do not inherit the caller's backup credentials or repository settings.
ENVIRONMENT = {
    key: value for key, value in os.environ.items()
    if not key.startswith("RESTIC_") and key != "NO_COLOR"
}


def restic(repo, *args, password=None, env=ENVIRONMENT):
    subprocess.run(
        ["restic", "--repo", str(repo),
         *(["--password-file", str(password)] if password else ["--insecure-no-password"]), *args],
        check=True,
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=sys.stderr,
    )


def configure_profiles(root, config, service_env, now):
    for name, target, scope in (
        ("default", "/mnt/backup/cairn", "machine"),
        ("nas", "/mnt/" + "personal-nas-folders/" * 8 + "office-archive", "machine"),
        ("cloud", "rest:https://fixture-username:fixture-secret@example.test/backups/cloud-projects"
                  "?key=fixture-token#fixture-fragment", "data-only"),
    ):
        folder = config if name == "default" else config / "profiles" / name
        folder.mkdir(parents=True)
        password = folder / "password"
        password.write_text("disposable-" + name + "-fixture-only")
        password.chmod(0o600)
        source = root / ("project-" + name)
        source.mkdir()
        note = source / (name + "-notes.md")
        note.write_text(name + " saved content\n")
        repo = root / ("repository-" + name)
        restic(repo, "init", password=password, env=service_env)
        stamp = (now - datetime.timedelta(hours=1)).strftime("%Y-%m-%d %H:%M:%S")
        restic(repo, "backup", str(source), "--host", "demo-laptop", "--tag", "snapshot",
               "--time", stamp, password=password, env=service_env)
        note.write_text(name + " modified content\n")
        folder.joinpath("config.json").write_text(json.dumps({
            "target": target, "repo": str(repo), "host": "demo-laptop", "backup_scope": scope,
            "snapshot": {"every": "1h", "keep": "24h,7d,4w", "paths": [str(source)]},
            "backup": {"every": "daily", "keep": "7d,4w,12m", "paths": [str(source)]},
        }))


def profile_receipt(root):
    control = {}
    for path in (root / "config").rglob("*"):
        if path.is_file():
            key = str(path.relative_to(root / "config"))
            control[key] = hashlib.sha256(path.read_bytes()).hexdigest()
    repos = {}
    for repo in root.glob("repository-*"):
        repos[repo.name] = sorted(p.name for p in (repo / "snapshots").iterdir())
    return {
        "control": control,
        "repos": repos,
        "systemctl": (root / "systemctl.log").read_text().splitlines() if (root / "systemctl.log").exists() else [],
        "notes": {p.name: p.read_text() for p in root.glob("project-*/*-notes.md")},
        "restic": (root / "restic.log").read_text().splitlines() if (root / "restic.log").exists() else [],
    }


def main():
    mode = sys.argv[2] if len(sys.argv) > 2 else "tui"
    overview = mode in {"profiles", "profiles-nas"}
    profile = "nas" if mode in {"profile", "profiles-nas"} else "cloud" if mode == "data-only" else "default"
    data_only = mode == "data-only"
    with tempfile.TemporaryDirectory(prefix="cairn-demo-") as directory:
        root = Path(directory)
        now = datetime.datetime.now().astimezone().replace(microsecond=0)
        if overview:
            # Only synthetic local repositories. Keep all inherited provider credentials out.
            safe_env = {"PATH": os.environ["PATH"], "HOME": str(root / "home"),
                        "LANG": "C.UTF-8", "TMPDIR": str(root)}
            (root / "home").mkdir()
            configure_profiles(root, root / "config/cairn", safe_env, now)
            workspace = root / ("project-" + profile)
        else:
            workspace = root / "projects"
            workspace.mkdir()
            (workspace / "notes.md").write_text("# Trail notes\n\nEverything has a way back.\n")
            (workspace / "launch-plan.md").write_text("# Launch plan\n\nShip the next milestone.\n")
            (workspace / "photos").mkdir()
            (workspace / "photos/README.txt").write_text("A place for the moments that matter.\n")
            repo = root / "repository"
            restic(repo, "init")
            for kind, ages in [("snapshot", [3, 2, 1, 0.2]), ("backup", [33, 9])]:
                for age in ages:
                    timestamp = (now - datetime.timedelta(hours=age)).strftime("%Y-%m-%d %H:%M:%S")
                    restic(
                        repo, "backup", str(workspace), "--host", "demo-laptop",
                        "--tag", kind, *(["--tag", "data-only"] if data_only and kind == "backup" else []),
                        "--time", timestamp,
                    )
            # The restore screen can now preview one modified file against a real snapshot.
            (workspace / "launch-plan.md").write_text("# Launch plan\n\nAn accidental edit.\n")

            config = root / "config/cairn"
            if profile != "default":
                config = config / "profiles" / profile
            config.mkdir(parents=True)
            config.joinpath("config.json").write_text(json.dumps({
                "target": "demo:projects (setup example)" if data_only else "/mnt/" + "personal-nas-folders/" * 10 + "cairn" if profile != "default" else "/mnt/backup/cairn",
                "repo": str(repo),
                "host": "demo-laptop",
                "backup_scope": "data-only" if data_only else "machine",
                "snapshot": {"every": "1h", "keep": "24h,7d,4w", "paths": [str(workspace)]},
                "backup": {"every": "daily", "keep": "7d,4w,12m", "paths": [str(workspace)]},
            }))
            if mode == "chooser":
                # Named-only setup: separate real local repositories, no default.
                saved = json.loads(config.joinpath("config.json").read_text())
                config.joinpath("config.json").unlink()
                for name in ["personal", "phin"]:
                    named = config / "profiles" / name
                    named.mkdir(parents=True)
                    recipe = dict(saved)
                    recipe["target"] = (
                        "/mnt/" + "personal-nas-folders/" * 10 + "PERSONAL"
                        if name == "personal" else "phin-example:BACKUPS/TARS (setup example)"
                    )
                    recipe["backup_scope"] = "data-only" if name == "personal" else "machine"
                    if name == "phin":
                        other_repo = root / "phin-repository"
                        restic(other_repo, "init")
                        recipe["repo"] = str(other_repo)
                    named.joinpath("config.json").write_text(json.dumps(recipe))
        tools = root / "bin"
        tools.mkdir()
        # Only scheduler metadata is a fixture; no host timers are read or changed.
        tools.joinpath("systemctl").write_text(
            "#!/bin/sh\n"
            + (f'printf "%s\\n" "$*" >> "{root / "systemctl.log"}"\n' if overview else "")
            + "case \"$*\" in\n"
            "  *--property=Version*) echo 258 ;;\n"
            "  *is-enabled*cairn-cloud-backup.timer*) echo disabled; exit 1 ;;\n"
            "  *is-enabled*cairn-nas-backup.timer*) exit 1 ;;\n"
            "  *is-enabled*) echo enabled ;;\n"
            "  *daemon-reload*|*enable*|*disable*|*start*) exit 0 ;;\n"
            "  *show*ActiveState*--value*) echo active ;;\n"
            "  *show*)\n"
            f"    next={int(now.timestamp()) + 48 * 60}\n"
            f"    case \"$*\" in *backup*) next={int(now.timestamp()) + 15 * 3600} ;; esac\n"
            "    printf 'NextElapseUSecRealtime=@%s\\nActiveState=active\\n\\n' \"$next\"\n"
            f"    printf 'ActiveState=inactive\\nResult=success\\nExecMainStatus=0\\nExecMainExitTimestamp=@{int(now.timestamp())}\\n'\n"
            "    ;;\n"
            "  *) exit 1 ;;\n"
            "esac\n"
        )
        tools.joinpath("systemctl").chmod(0o700)
        if overview:
            real_restic = shutil.which("restic", path=safe_env["PATH"])
            assert real_restic
            wrapper = tools / "restic"
            wrapper.write_text(
                "#!/bin/sh\n"
                f'printf "%s|%s\\n" "$RESTIC_REPOSITORY" "$*" >> {shlex.quote(str(root / "restic.log"))}\n'
                'if [ "${CAIRN_DEMO_JOB:-}" = "hold" ]; then\n'
                '  case " $* " in *" backup "*)\n'
                f'    : > {shlex.quote(str(root / "job-ready"))}\n'
                f'    while [ ! -f {shlex.quote(str(root / "job-release"))} ]; do /bin/sleep .05; done\n'
                '  ;; esac\n'
                'fi\n'
                f'exec {shlex.quote(real_restic)} "$@"\n'
            )
            wrapper.chmod(0o700)
        env = {
            **(safe_env if overview else ENVIRONMENT),
            "PATH": str(tools) + os.pathsep + os.environ["PATH"],
            "XDG_CONFIG_HOME": str(root / "config"),
            "XDG_STATE_HOME": str(root / "state"),
            "XDG_DATA_HOME": str(root / "data"),
            "XDG_CACHE_HOME": str(root / "cache"),
            "XDG_RUNTIME_DIR": str(root / "runtime"),
            "TERM": "xterm-256color",
            "COLORTERM": "truecolor",
            "CLICOLOR_FORCE": "1",
        }
        Path(env["XDG_RUNTIME_DIR"]).mkdir()
        master, slave = pty.openpty()
        rows, cols = (24, 40) if mode == "profiles-nas" else (32, 110)
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        process = subprocess.Popen(
            [sys.argv[1], *(["--profile", profile] if profile != "default" else []),
             *(["restore", "launch-plan.md"] if mode == "restore" else [])],
            env=env, cwd=workspace, stdin=slave, stdout=slave, stderr=slave,
            start_new_session=True,
        )
        os.close(slave)
        signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
        input_fd = sys.stdin.fileno()
        pending_input = b""
        background = None
        try:
            while process.poll() is None:
                ready, _, _ = select.select([master, input_fd], [], [], 0.2)
                if master in ready:
                    try:
                        data = os.read(master, 65536)
                    except OSError:
                        break
                    if not data:
                        break
                    print(json.dumps({"output": base64.b64encode(data).decode()}), flush=True)
                if input_fd in ready:
                    data = os.read(input_fd, 65536)
                    if not data:
                        break
                    pending_input += data
                while b"\n" in pending_input:
                    line, pending_input = pending_input.split(b"\n", 1)
                    message = json.loads(line)
                    if overview and "receipt" in message:
                        print(json.dumps({"receipt": profile_receipt(root)}), flush=True)
                    elif overview and "undo" in message:
                        # Snapshot ID comes from the isolated repository, not from external input.
                        snapshot = message["undo"]
                        assert len(snapshot) == 64 and all(c in "0123456789abcdef" for c in snapshot)
                        note = root / "project-cloud/cloud-notes.md"
                        result = subprocess.run(
                            [sys.argv[1], "--profile", "cloud", "restore", "--at", snapshot, "-y", "--", str(note)],
                            env=env, cwd=workspace, text=True, capture_output=True, check=True, timeout=20,
                        )
                        assert "restored" in result.stdout.lower()
                        print(json.dumps({"receipt": profile_receipt(root)}), flush=True)
                    elif overview and "background" in message:
                        if message["background"] == "start":
                            # Execute the exact service command Cairn wrote for the selected
                            # profile, independently of both the TUI and any host manager.
                            service = root / "config/systemd/user/cairn-cloud-snapshot.service"
                            exec_start = next(line.removeprefix("ExecStart=") for line in
                                              service.read_text().splitlines() if line.startswith("ExecStart="))
                            background = subprocess.Popen(
                                shlex.split(exec_start), env={**env, "CAIRN_DEMO_JOB": "hold"},
                                cwd=workspace, stdin=subprocess.DEVNULL,
                                stdout=subprocess.DEVNULL, stderr=sys.stderr,
                            )
                            deadline = time.monotonic() + 10
                            while not (root / "job-ready").exists():
                                if background.poll() is not None or time.monotonic() >= deadline:
                                    raise RuntimeError("The isolated background snapshot never reached its backup.")
                                time.sleep(.02)
                        elif message["background"] == "finish":
                            assert background is not None and background.poll() is None
                            (root / "job-release").touch()
                            assert background.wait(timeout=20) == 0
                        print(json.dumps({"receipt": profile_receipt(root)}), flush=True)
                    elif "resize" in message:
                        rows, cols = message["resize"]
                        fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
                        process.send_signal(signal.SIGWINCH)
                    else:
                        os.write(master, message["input"].encode())
        finally:
            if background is not None and background.poll() is None:
                (root / "job-release").touch()
                try:
                    background.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    background.terminate()
                    background.wait(timeout=5)
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
            os.close(master)


if __name__ == "__main__":
    main()
