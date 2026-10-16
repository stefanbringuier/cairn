"""Run the real TUI in a PTY backed by disposable files and a restic repo."""

import base64
import datetime
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios

# Do not inherit the caller's backup credentials or repository settings.
ENVIRONMENT = {
    key: value for key, value in os.environ.items()
    if not key.startswith("RESTIC_") and key != "NO_COLOR"
}


def restic(repo, *args):
    subprocess.run(
        ["restic", "--repo", str(repo), "--insecure-no-password", *args],
        check=True,
        env=ENVIRONMENT,
        stdout=subprocess.DEVNULL,
        stderr=sys.stderr,
    )


def main():
    mode = sys.argv[2] if len(sys.argv) > 2 else "tui"
    profile = "nas" if mode == "profile" else "cloud" if mode == "data-only" else "default"
    data_only = mode == "data-only"
    with tempfile.TemporaryDirectory(prefix="cairn-demo-") as directory:
        root = Path(directory)
        workspace = root / "projects"
        workspace.mkdir()
        (workspace / "notes.md").write_text("# Trail notes\n\nEverything has a way back.\n")
        (workspace / "launch-plan.md").write_text("# Launch plan\n\nShip the next milestone.\n")
        (workspace / "photos").mkdir()
        (workspace / "photos/README.txt").write_text("A place for the moments that matter.\n")
        repo = root / "repository"
        restic(repo, "init")
        now = datetime.datetime.now().astimezone().replace(microsecond=0)
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
            "case \"$*\" in\n"
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
        env = {
            **ENVIRONMENT,
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
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 32, 110, 0, 0))
        process = subprocess.Popen(
            [sys.argv[1], *(["--profile", profile] if profile != "default" else []),
             *(["restore", "launch-plan.md"] if mode == "restore" else [])],
            env=env, cwd=workspace, stdin=slave, stdout=slave, stderr=slave,
            start_new_session=True,
        )
        os.close(slave)
        signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
        try:
            while process.poll() is None:
                ready, _, _ = select.select([master, sys.stdin], [], [], 0.2)
                if master in ready:
                    try:
                        data = os.read(master, 65536)
                    except OSError:
                        break
                    if not data:
                        break
                    print(json.dumps({"output": base64.b64encode(data).decode()}), flush=True)
                if sys.stdin in ready:
                    line = sys.stdin.readline()
                    if not line:
                        break
                    message = json.loads(line)
                    if "resize" in message:
                        rows, cols = message["resize"]
                        fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
                        process.send_signal(signal.SIGWINCH)
                    else:
                        os.write(master, message["input"].encode())
        finally:
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
