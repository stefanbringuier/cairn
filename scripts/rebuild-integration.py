"""Exercise rebuild path mapping with real restic and disposable, isolated homes.

Scheduler/package executables are inert fixtures. No host timers, credentials,
repositories, or package managers are accessed. Pass a built cairn executable;
CAIRN_RESTIC may select an existing test restic (otherwise use PATH or cairn's
checksum-verified bootstrap inside this script's temporary directory).
"""

import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import shlex
import shutil
import struct
import subprocess
import sys
import tempfile
import termios
import time


BINARY = str(Path(sys.argv[1]).resolve())
ANSI = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")


def executable(path, contents):
    path.write_text(contents)
    path.chmod(0o700)


def isolated_env(root):
    """Only carry generic process settings, never account/provider variables."""
    env = {key: os.environ[key] for key in ("PATH", "LANG", "LC_ALL", "TERM", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy") if key in os.environ}
    env.update({"HOME": str(root / "home"), "XDG_CONFIG_HOME": str(root / "config"),
                "XDG_STATE_HOME": str(root / "state"), "XDG_DATA_HOME": str(root / "data"),
                "XDG_CACHE_HOME": str(root / "cache"), "XDG_RUNTIME_DIR": str(root / "runtime"),
                "TMPDIR": str(root / "tmp"), "NO_COLOR": "1"})
    for key in ("HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME",
                "XDG_CACHE_HOME", "XDG_RUNTIME_DIR", "TMPDIR"):
        Path(env[key]).mkdir(parents=True, exist_ok=True)
    return env


def acquire_restic(root):
    selected = os.environ.get("CAIRN_RESTIC") or shutil.which("restic")
    if selected:
        selected = str(Path(selected).resolve())
        subprocess.run([selected, "version"], check=True, capture_output=True, text=True, timeout=15)
        return selected
    bootstrap = root / "bootstrap"
    env = isolated_env(bootstrap)
    tools = bootstrap / "tools"
    tools.mkdir()
    for name in ("systemctl", "loginctl"):
        executable(tools / name, "#!/bin/sh\nexit 1\n")
    env["PATH"] = str(tools) + os.pathsep + env.get("PATH", "")
    result = subprocess.run([BINARY, "init", str(bootstrap / "repo"), "--data-only", "-y"],
                            env=env, cwd=bootstrap, stdin=subprocess.DEVNULL,
                            capture_output=True, text=True, timeout=180)
    assert result.returncode == 0, ("restic bootstrap", result.stdout, result.stderr)
    selected = bootstrap / "data/cairn/bin/restic"
    assert selected.is_file(), "cairn did not bootstrap its private restic executable"
    return str(selected)


class Fixture:
    def __init__(self, root, real_restic, *, data_only=False):
        self.root = root
        self.env = isolated_env(root)
        self.home = root / "home"
        self.repo = root / "repository"
        self.tools = root / "tools"
        self.tools.mkdir()
        self.config_dir = root / "config/cairn/profiles/phin"
        self.config_dir.mkdir(parents=True)
        self.config = self.config_dir / "config.json"
        self.password = self.config_dir / "password"
        self.password.write_text("disposable issue 14 fixture password\n")
        self.password.chmod(0o600)
        self.restic_log = root / "restic.jsonl"
        self.scheduler_log = root / "systemctl.jsonl"
        self.package_log = root / "package-calls"
        self.enabled = root / "runtime/enabled"
        self.enabled.mkdir()
        self.old = root / "old data/PHIN"
        self.second = root / "old data/SECOND"
        self.dest = root / "new disk/PHIN"
        self.second_dest = root / "new disk/SECOND"
        for directory in (self.old, self.second):
            (directory / "nested").mkdir(parents=True)
            (directory / "notes.txt").write_text(f"saved {directory.name}\n")
            (directory / "nested/child.txt").write_text("saved nested bytes\n")
        self.real_restic = real_restic
        self.env.update({"CAIRN_FIXTURE_RESTIC": real_restic,
                         "CAIRN_FIXTURE_RESTIC_LOG": str(self.restic_log),
                         "CAIRN_FIXTURE_SCHEDULER_LOG": str(self.scheduler_log),
                         "CAIRN_FIXTURE_ENABLED": str(self.enabled),
                         "CAIRN_FIXTURE_PACKAGE_LOG": str(self.package_log)})
        executable(self.tools / "restic", f"#!{sys.executable}\n" + '''import json, os, sys
args = sys.argv[1:]
with open(os.environ["CAIRN_FIXTURE_RESTIC_LOG"], "a") as stream:
    stream.write(json.dumps(args) + "\\n")
fail = os.environ.get("CAIRN_FIXTURE_FAIL_SOURCE")
if fail and ("restore" in args or "dump" in args):
    base = "/" + os.path.basename(fail)
    included = "--include" in args and args[args.index("--include") + 1] == base
    direct = any(arg == fail or arg.endswith(":" + fail) for arg in args)
    if included or direct:
        print("injected second-mapping restore failure", file=sys.stderr)
        sys.exit(42)
os.execv(os.environ["CAIRN_FIXTURE_RESTIC"], [os.environ["CAIRN_FIXTURE_RESTIC"], *args])
''')
        executable(self.tools / "systemctl", f"#!{sys.executable}\n" + '''import json, os, pathlib, sys
args = sys.argv[1:]
with open(os.environ["CAIRN_FIXTURE_SCHEDULER_LOG"], "a") as stream:
    stream.write(json.dumps(args) + "\\n")
args = [arg for arg in args if arg != "--user"]
enabled = pathlib.Path(os.environ["CAIRN_FIXTURE_ENABLED"])
if args and args[0] in ("enable", "disable"):
    for unit in args[1:]:
        if unit.endswith(".timer"):
            path = enabled / unit
            if args[0] == "enable": path.touch()
            else: path.unlink(missing_ok=True)
elif args and args[0] == "show":
    print("active" if (enabled / args[-1]).exists() else "inactive")
''')
        executable(self.tools / "loginctl", "#!/bin/sh\nexit 0\n")
        for name in ("sudo", "apt", "apt-mark", "apt-cache", "snap", "flatpak", "brew", "uv",
                     "pipx", "cargo", "npm", "crontab"):
            executable(self.tools / name, '#!/bin/sh\nprintf "%s\\n" "$0 $*" >> "$CAIRN_FIXTURE_PACKAGE_LOG"\nexit 99\n')
        self.env["PATH"] = str(self.tools) + os.pathsep + self.env.get("PATH", "")
        self.restic("init")
        self.saved_id = self.backup([self.old, self.second], data_only=data_only)
        config = {"target": str(self.repo), "repo": str(self.repo), "host": "new-box",
                  "backup_scope": "data-only" if data_only else "machine",
                  "since": "2021-01-01T00:00:00Z",
                  "snapshot": {"every": "1h", "keep": "100,24h", "paths": [str(self.old), str(self.old / "nested")]},
                  "backup": {"every": "daily", "keep": "100,7d", "paths": [str(self.old), str(self.second)]},
                  "exclude": ["*.skip"]}
        self.config.write_text(json.dumps(config, indent=2) + "\n")
        self.reset_logs()

    def restic(self, *args):
        result = subprocess.run([self.real_restic, "--repo", str(self.repo), "--password-file", str(self.password), *map(str, args)],
                                env=self.env, cwd=self.root, stdin=subprocess.DEVNULL,
                                capture_output=True, text=True, timeout=120)
        assert result.returncode == 0, ("fixture restic", args, result.returncode, result.stdout, result.stderr)
        return result.stdout

    def backup(self, paths, *, host="old-box", date="2020-01-01 12:00:00", tag="backup", data_only=False):
        args = ["backup", "--json", "--host", host, "--time", date]
        for value in (tag,) if isinstance(tag, str) else tag:
            args += ["--tag", value]
        if data_only:
            args += ["--tag", "data-only"]
        output = self.restic(*args, "--", *paths)
        return json.loads(output.splitlines()[-1])["snapshot_id"]

    def saved_home(self):
        """Include an old config that would overwrite the current one if restored."""
        config_home = self.home / ".config"
        destination = config_home / "cairn/profiles/phin"
        destination.parent.mkdir(parents=True)
        shutil.move(str(self.config_dir), destination)
        self.config_dir = destination
        self.config = destination / "config.json"
        self.password = destination / "password"
        self.env["XDG_CONFIG_HOME"] = str(config_home)
        old_home = self.root / "old home"
        saved_config = old_home / ".config/cairn/profiles/phin"
        saved_config.mkdir(parents=True)
        old_home.joinpath("home-notes.txt").write_text("saved home bytes\n")
        self.home.joinpath("home-notes.txt").write_text("current home bytes\n")
        config = json.loads(self.config.read_text())
        config["host"] = "old-box"
        config["since"] = "2018-01-01T00:00:00Z"
        config["snapshot"]["paths"] = [str(old_home), str(self.old / "nested")]
        config["backup"]["paths"] = [str(old_home), str(self.old)]
        saved_config.joinpath("config.json").write_text(json.dumps(config) + "\n")
        saved_config.joinpath("password").write_bytes(self.password.read_bytes())
        saved_config.joinpath("password").chmod(0o600)
        self.saved_home_id = self.backup([old_home], date="2020-01-05 12:00:00")
        self.reset_logs()
        return old_home

    def run(self, *args, expected=0):
        result = subprocess.run([BINARY, "--profile", "phin", *map(str, args)], env=self.env, cwd=self.root,
                                stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=120)
        if expected is None:
            assert result.returncode != 0, (args, "unexpected success", result.stdout, result.stderr)
        else:
            assert result.returncode == expected, (args, result.returncode, result.stdout, result.stderr)
        return ANSI.sub("", result.stdout + result.stderr)

    def rebuild(self, mappings=None, *extra, expected=0, no_home=True):
        args = ["rebuild", "--host", "old-box"]
        if no_home:
            args.append("--no-home")
        for source, dest in (mappings if mappings is not None else [(self.old, self.dest)]):
            args += ["--map", f"{source}={dest}"]
        return self.run(*args, *extra, expected=expected)

    def reset_logs(self):
        self.restic_log.write_text("")
        self.scheduler_log.write_text("")
        self.package_log.write_text("")

    def calls(self):
        return [json.loads(line) for line in self.restic_log.read_text().splitlines()]

    def snapshots(self):
        return json.loads(self.restic("snapshots", "--json"))

    def assert_no_mutation(self, before_config, before_ids):
        assert self.config.read_bytes() == before_config, "preflight changed configuration"
        assert [snap["id"] for snap in self.snapshots()] == before_ids, "preflight wrote a repository snapshot"
        assert not any(any(command in call for command in ("backup", "restore", "dump", "forget", "prune")) for call in self.calls()), self.calls()
        assert not self.scheduler_log.read_text(), "preflight touched scheduler"
        assert not self.package_log.read_text(), "preflight invoked package commands"

    def undo_commands(self, output):
        return re.findall(r"cairn --profile phin restore[^\r\n]*", output)

    def execute_undo(self, command):
        args = shlex.split(command)
        assert args[:3] == ["cairn", "--profile", "phin"], command
        return self.run(*args[3:])


def cancel_in_terminal(fixture, command=None, *, term):
    if command is None:
        command = ["rebuild", "--host", "old-box", "--no-home", "--map", f"{fixture.old}={fixture.dest}"]
    args = [BINARY, "--profile", "phin", *map(str, command)]
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 32, 110, 0, 0))
    env = {**fixture.env, "TERM": term}
    process = subprocess.Popen(args, env=env, cwd=fixture.root, stdin=slave,
                               stdout=slave, stderr=slave, start_new_session=True)
    os.close(slave)
    output = bytearray()
    answered = False
    try:
        deadline = time.monotonic() + 30
        while process.poll() is None and time.monotonic() < deadline:
            if select.select([master], [], [], 0.1)[0]:
                try:
                    data = os.read(master, 65536)
                except OSError:
                    break
                if not data:
                    break
                output.extend(data)
                if not answered and (b"Cancel" in output or b"[y/N]" in output):
                    # Huh's full form needs an actual negative selection plus
                    # Enter; Escape is not its cancellation key. CR also works
                    # in the plain fallback through the PTY's input mapping.
                    os.write(master, b"n\r")
                    answered = True
        diagnostic = ("TERM=" + term, args, output.decode(errors="replace"))
        assert answered, ("no confirmation", diagnostic)
        expected_prompt = b"[y/N]" if term == "dumb" else b"Cancel"
        assert expected_prompt in output, ("unexpected terminal prompt mode", diagnostic)
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired as error:
            raise AssertionError(("negative confirmation did not exit", diagnostic)) from error
        assert process.returncode == 1, ("unexpected cancellation exit", process.returncode, diagnostic)
    finally:
        if process.poll() is None:
            process.kill()
            process.wait()
        os.close(master)
    return ANSI.sub("", output.decode(errors="replace"))


def main():
    with tempfile.TemporaryDirectory(prefix="cairn-rebuild-") as directory:
        root = Path(directory)
        real_restic = acquire_restic(root)

        f = Fixture(root / "machine", real_restic)
        output = f.rebuild(None, "-y")
        assert f.dest.joinpath("notes.txt").read_text() == "saved PHIN\n"
        assert f.dest.joinpath("nested/child.txt").read_text() == "saved nested bytes\n"
        assert not f.dest.joinpath("PHIN").exists(), "--map treated exact destination as a container"
        config = json.loads(f.config.read_text())
        assert config["host"] == "old-box", config
        assert config["snapshot"]["paths"] == [str(f.dest), str(f.dest / "nested")], config
        assert config["backup"]["paths"] == [str(f.dest), str(f.second)], config
        assert "old-box" in output and str(f.old) in output and str(f.dest) in output, output
        assert "--map" in output and str(f.second) in output, ("missing unmapped-path guidance", output)
        calls = [json.loads(line) for line in f.scheduler_log.read_text().splitlines()]
        assert any("daemon-reload" in call for call in calls), calls
        assert (f.enabled / "cairn-phin-snapshot.timer").exists()
        assert (f.enabled / "cairn-phin-backup.timer").exists()
        assert not f.package_log.read_text(), "recipe-free mapping invoked package commands"
        assert not (f.root / "state/cairn/profiles/phin/rebuild/etc").exists()
        print("Passed: renamed host, exact path relocation, child-path rewrite, unmapped guidance and isolated schedule reapply.", flush=True)

        f = Fixture(root / "data-only", real_restic, data_only=True)
        f.rebuild(None, "-y", no_home=False)
        assert f.dest.joinpath("notes.txt").read_text() == "saved PHIN\n"
        assert json.loads(f.config.read_text())["host"] == "old-box"
        assert not f.package_log.read_text()
        assert not (f.root / "state/cairn/profiles/phin/rebuild").exists()
        assert not list(f.home.iterdir()), "data-only mapping restored HOME"
        print("Passed: data-only backup mapping without explicit no-home, no home/package/etc effects.", flush=True)

        f = Fixture(root / "preflight", real_restic)
        before = f.config.read_bytes()
        ids = [snap["id"] for snap in f.snapshots()]
        f.rebuild(expected=2)
        f.assert_no_mutation(before, ids)
        assert not f.dest.exists()
        for term in ("xterm-256color", "dumb"):
            cancel_in_terminal(f, term=term)
            f.assert_no_mutation(before, ids)
            assert not f.dest.exists(), term
        # Invalid second entries must be rejected before even a valid first mapping writes.
        forbidden = [(f.root / "unknown", f.second_dest),
                     (f.second, f.dest / "nested"),
                     (f.second, f.repo / "restored"),
                     (f.second, f.root / "config/cairn/restore"),
                     (f.second, f.old / "nested/new"),
                     (f.old, f.second_dest)]
        for source, dest in forbidden:
            f.reset_logs()
            f.rebuild([(f.old, f.dest), (source, dest)], "-y", expected=None)
            f.assert_no_mutation(before, ids)
            assert not f.dest.exists(), ("first mapping ran before full preflight", source, dest)
        for mapping in ("relative=/tmp/not-used", f"{f.old}=relative", "not-a-map"):
            f.reset_logs()
            f.run("rebuild", "--host", "old-box", "--no-home", "--map", mapping, "-y", expected=None)
            f.assert_no_mutation(before, ids)
        alias = f.root / "repository-alias"
        alias.symlink_to(f.repo, target_is_directory=True)
        f.reset_logs()
        f.rebuild([(f.old, alias / "restored")], "-y", expected=None)
        f.assert_no_mutation(before, ids)
        # Existing symlink destinations and external credential targets retain
        # the ordinary restore protection even when XDG is outside HOME.
        symlink_dest = f.root / "destination-link"
        symlink_dest.symlink_to(f.second, target_is_directory=True)
        f.reset_logs()
        f.rebuild([(f.old, symlink_dest)], "-y", expected=None)
        f.assert_no_mutation(before, ids)
        assert symlink_dest.is_symlink()
        secrets = f.root / "external-secrets"
        secrets.mkdir()
        external_password = secrets / "password"
        f.password.rename(external_password)
        f.password.symlink_to(external_password)
        password_bytes = external_password.read_bytes()
        f.reset_logs()
        f.rebuild([(f.old, secrets)], "-y", expected=None)
        f.assert_no_mutation(before, ids)
        assert external_password.read_bytes() == password_bytes and f.password.is_symlink()
        print("Passed: no-TTY refusal, xterm/dumb real-PTY cancellation and all-mapping validation/protected path checks before writes.", flush=True)

        f = Fixture(root / "old-home-alias", real_restic)
        old_home = f.saved_home()
        alias = f.root / "old-home-alias"
        alias.symlink_to(old_home, target_is_directory=True)
        before = f.config.read_bytes()
        ids = [snap["id"] for snap in f.snapshots()]
        output = f.rebuild([(f.old, f.dest), (f.second, alias / "restored")],
                           "--from", old_home, "-y", no_home=False, expected=None)
        assert "old home" in output, output
        f.assert_no_mutation(before, ids)
        assert not f.dest.exists() and not (old_home / "restored").exists()
        assert f.home.joinpath("home-notes.txt").read_text() == "current home bytes\n"
        for source in (old_home, old_home / ".config"):
            f.reset_logs()
            f.rebuild([(f.old, f.dest), (source, f.second_dest)],
                      "--from", old_home, "-y", no_home=False, expected=None)
            f.assert_no_mutation(before, ids)
            assert not f.dest.exists() and not f.second_dest.exists()
        print("Passed: canonical old-home alias destinations and duplicate/overlapping home sources rejected before writes.", flush=True)

        f = Fixture(root / "undo", real_restic)
        f.dest.mkdir(parents=True)
        (f.dest / "notes.txt").write_text("unsaved current destination\n")
        (f.dest / "secret.skip").write_text("normally excluded bytes\n")
        output = f.rebuild(None, "-y")
        assert f.dest.joinpath("notes.txt").read_text() == "saved PHIN\n"
        assert not f.dest.joinpath("PHIN").exists(), "existing directory treated as a container"
        safety = [snap for snap in f.snapshots() if "pre-restore" in snap.get("tags", [])]
        assert len(safety) == 1 and safety[0]["paths"] == [str(f.dest)], safety
        assert f.restic("dump", safety[0]["id"], f.dest / "secret.skip") == "normally excluded bytes\n"
        commands = f.undo_commands(output)
        assert commands, output
        f.execute_undo(commands[0])
        assert f.dest.joinpath("notes.txt").read_text() == "unsaved current destination\n"
        assert f.dest.joinpath("secret.skip").read_text() == "normally excluded bytes\n"
        print("Passed: complete existing-destination safety snapshot and executable undo after old-host adoption.", flush=True)

        f = Fixture(root / "partial-failure", real_restic)
        f.dest.mkdir(parents=True)
        (f.dest / "notes.txt").write_text("first destination before rebuild\n")
        before = f.config.read_bytes()
        f.env["CAIRN_FIXTURE_FAIL_SOURCE"] = str(f.second)
        output = f.rebuild([(f.old, f.dest), (f.second, f.second_dest)], "-y", expected=None)
        assert "injected second-mapping" in output, output
        assert f.dest.joinpath("notes.txt").read_text() == "saved PHIN\n"
        assert not f.second_dest.exists(), "failed new destination published partial content"
        assert f.config.read_bytes() == before, "partial failure adopted host or paths"
        assert not f.scheduler_log.read_text(), "partial failure changed timers"
        commands = f.undo_commands(output)
        assert commands and str(f.dest) in output, output
        del f.env["CAIRN_FIXTURE_FAIL_SOURCE"]
        f.execute_undo(commands[0])
        assert f.dest.joinpath("notes.txt").read_text() == "first destination before rebuild\n"
        print("Passed: second-mapping failure retains completed restore, unchanged configuration/timers and usable undo.", flush=True)

        f = Fixture(root / "selection", real_restic)
        f.old.joinpath("notes.txt").write_text("latest eligible old-host bytes\n")
        eligible = f.backup([f.old], date="2020-01-02 12:00:00", tag="snapshot")
        f.old.joinpath("notes.txt").write_text("wrong host bytes\n")
        f.backup([f.old], host="another-box", date="2020-01-03 12:00:00")
        f.old.joinpath("notes.txt").write_text("pre-restore not a rebuild source\n")
        f.backup([f.old], date="2020-01-04 12:00:00", tag="pre-restore")
        f.old.joinpath("notes.txt").write_text("mixed backup and pre-restore tags must not win\n")
        mixed = f.backup([f.old], date="2020-01-05 12:00:00", tag=("backup", "pre-restore"))
        assert set(next(snap for snap in f.snapshots() if snap["id"] == mixed)["tags"]) == {"backup", "pre-restore"}
        f.old.joinpath("notes.txt").write_text("after init must not win\n")
        f.backup([f.old], date="2022-01-01 12:00:00")
        output = f.rebuild(None, "-y")
        assert f.dest.joinpath("notes.txt").read_text() == "latest eligible old-host bytes\n", output
        explicit = f.root / "explicit version"
        f.rebuild([(f.old, explicit)], "--at", f.saved_id, "-y")
        assert explicit.joinpath("notes.txt").read_text() == "saved PHIN\n"
        dated = f.root / "dated version"
        f.rebuild([(f.old, dated)], "--at", "2020-01-01", "-y")
        assert dated.joinpath("notes.txt").read_text() == "saved PHIN\n"
        assert eligible[:8] in output, output
        print("Passed: latest eligible old-host source, since and single/mixed-tag pre-restore exclusion, exact snapshot and date selection.", flush=True)

        f = Fixture(root / "home-and-mapping", real_restic)
        old_home = f.saved_home()
        before = json.loads(f.config.read_text())
        output = f.rebuild(None, "--from", old_home, "--at", f.saved_id,
                           "--no-packages", "-y", no_home=False)
        home_plan = next((line for line in output.splitlines() if "home:" in line), "")
        assert f.saved_home_id[:8] in home_plan and "backup" in home_plan, output
        assert f.dest.joinpath("notes.txt").read_text() == "saved PHIN\n"
        assert f.home.joinpath("home-notes.txt").read_text() == "saved home bytes\n"
        config = json.loads(f.config.read_text())
        assert config["host"] == "old-box", config
        assert config["since"] == before["since"] and config["target"] == before["target"], config
        assert config["snapshot"]["paths"] == [str(f.home), str(f.dest / "nested")], config
        assert config["backup"]["paths"] == [str(f.home), str(f.dest)], config
        assert not f.package_log.read_text()
        assert (f.enabled / "cairn-phin-snapshot.timer").exists()
        print("Passed: combined --from plus external mapping restores home/config, remaps both roots and preserves connection cutoff.", flush=True)

        f = Fixture(root / "mapping-before-home", real_restic)
        old_home = f.saved_home()
        before = f.config.read_bytes()
        password = f.password.read_bytes()
        f.env["CAIRN_FIXTURE_FAIL_SOURCE"] = str(f.second)
        output = f.rebuild([(f.old, f.dest), (f.second, f.second_dest)],
                           "--from", old_home, "--no-packages", "-y", no_home=False, expected=None)
        assert "injected second-mapping" in output, output
        assert f.dest.joinpath("notes.txt").read_text() == "saved PHIN\n"
        assert not f.second_dest.exists()
        assert f.config.read_bytes() == before and f.password.read_bytes() == password
        assert f.home.joinpath("home-notes.txt").read_text() == "current home bytes\n"
        assert not any(old_home.as_posix() in argument for call in f.calls()
                       if "restore" in call or "dump" in call for argument in call), f.calls()
        assert not f.scheduler_log.read_text() and not f.package_log.read_text()
        print("Passed: mapping failure precedes whole-home restore and preserves current in-home configuration/password and timers.", flush=True)

        f = Fixture(root / "fresh-package-stage", real_restic)
        manifests = f.root / "saved state/cairn/profiles/phin/manifests"
        manifests.mkdir(parents=True)
        manifests.joinpath("flatpak.txt").write_text("fixture-flatpak\n")
        f.backup([manifests], date="2020-01-06 12:00:00")
        stale = f.root / "state/cairn/profiles/phin/rebuild/etc/apt/sources.list.d/stale.list"
        stale.parent.mkdir(parents=True)
        stale.write_text("disposable stale apt source; never use\n")
        stale_key = f.root / "state/cairn/profiles/phin/rebuild/keyrings/stale.gpg"
        stale_key.parent.mkdir()
        stale_key.write_text("disposable stale key; never use\n")
        f.reset_logs()
        output = f.rebuild(None, "-y")
        assert f.dest.joinpath("notes.txt").read_text() == "saved PHIN\n"
        calls = f.package_log.read_text().splitlines()
        assert calls == [f"{f.tools / 'flatpak'} install -y fixture-flatpak"], (calls, output)
        assert "Bring back third-party apt sources" not in output, output
        assert stale.read_text() == "disposable stale apt source; never use\n"
        assert stale_key.read_text() == "disposable stale key; never use\n"
        print("Passed: manifests-only recipe uses fresh per-run staging and ignores stale apt sources/keyrings; package fixture is inert.", flush=True)

        f = Fixture(root / "reinitialize", real_restic, data_only=True)
        f.rebuild(None, "-y", no_home=False)
        before = f.config.read_bytes()
        password = f.password.read_bytes()
        ids = [snap["id"] for snap in f.snapshots()]
        f.reset_logs()
        f.run("init", f.repo, expected=2)
        f.assert_no_mutation(before, ids)
        assert f.password.read_bytes() == password
        for term in ("xterm-256color", "dumb"):
            prompt = cancel_in_terminal(f, ["init", f.repo], term=term)
            assert "old-box" in prompt and os.uname().nodename in prompt, (term, prompt)
            f.assert_no_mutation(before, ids)
            assert f.password.read_bytes() == password, term
        # A second real disposable repository has a different key. Reconnecting
        # fails without publishing the in-memory host/target changes or timers.
        locked_repo = f.root / "different-key-repository"
        locked_password = f.root / "different-key-password"
        locked_password.write_text("different disposable repository password\n")
        locked_password.chmod(0o600)
        result = subprocess.run([real_restic, "--repo", str(locked_repo), "--password-file", str(locked_password), "init"],
                                env=f.env, cwd=f.root, stdin=subprocess.DEVNULL,
                                capture_output=True, text=True, timeout=120)
        assert result.returncode == 0, (result.returncode, result.stdout, result.stderr)
        f.run("init", locked_repo, "-y", expected=None)
        f.assert_no_mutation(before, ids)
        assert f.password.read_bytes() == password
        f.run("init", f.repo, "-y")
        previous = json.loads(before)
        config = json.loads(f.config.read_text())
        assert config["host"] == os.uname().nodename and config["host"] != previous["host"], config
        assert config["since"] != previous["since"], config
        for key in ("target", "repo", "backup_scope", "snapshot", "backup", "exclude"):
            assert config[key] == previous[key], (key, previous, config)
        assert f.password.read_bytes() == password
        assert (f.enabled / "cairn-phin-snapshot.timer").exists()
        f.run("snapshot")
        snapshots = f.snapshots()
        assert all(any(snap["id"] == old_id for snap in snapshots) for old_id in ids), snapshots
        assert any(snap["hostname"] == os.uname().nodename for snap in snapshots), snapshots
        assert not f.package_log.read_text()
        print("Passed: reinit xterm/dumb cancellation/no-TTY and real wrong-key refusal preserve config/password/timers; success starts current-host series retaining settings and history.", flush=True)

        f = Fixture(root / "legacy", real_restic)
        old_home = f.root / "legacy-old-home"
        old_home.mkdir()
        old_home.joinpath("old-home.txt").write_text("legacy home restore\n")
        f.backup([old_home], date="2020-01-05 12:00:00")
        f.run("rebuild", "--host", "old-box", "--from", old_home, "-y")
        assert f.home.joinpath("old-home.txt").read_text() == "legacy home restore\n"
        assert json.loads(f.config.read_text())["host"] == "old-box"
        assert not f.package_log.read_text()
        print("Passed: existing machine rebuild path without new flags remains functional in isolated HOME.", flush=True)


if __name__ == "__main__":
    main()
