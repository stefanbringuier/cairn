"""Exercise the real CLI and restic without accessing a user's backup setup."""

import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import shutil
import struct
import subprocess
import sys
import tempfile
import termios
import time

BINARY = str(Path(sys.argv[1]).resolve())


def interactive(args, env, cwd, reply):
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 32, 110, 0, 0))
    process = subprocess.Popen([BINARY, *args], env=env, cwd=cwd,
                               stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
    os.close(slave)
    output, answered = bytearray(), False
    deadline = time.monotonic() + 30
    try:
        while process.poll() is None and time.monotonic() < deadline:
            if select.select([master], [], [], 0.1)[0]:
                try:
                    data = os.read(master, 65536)
                except OSError:
                    break
                output.extend(data)
                if b"Restore " in output and b"notes.txt" in output and not answered:
                    # Wait for the complete form before entering the answer.
                    if b"Cancel" in output:
                        os.write(master, reply)
                        answered = True
        assert answered, "Interactive restore did not show the expected confirmation"
        process.wait(timeout=5)
        while select.select([master], [], [], 0.1)[0]:
            try:
                data = os.read(master, 65536)
            except OSError:
                break
            if not data:
                break
            output.extend(data)
        return process.returncode, output.decode(errors="replace")
    finally:
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=5)
        os.close(master)


def main():
    with tempfile.TemporaryDirectory(prefix="cairn-integration-") as directory:
        root = Path(directory)
        workspace = root / "projects"
        workspace.mkdir()
        tools = root / "bin"
        tools.mkdir()
        for name in ("systemctl", "loginctl"):
            tools.joinpath(name).write_text("#!/bin/sh\nexit 1\n")
            tools.joinpath(name).chmod(0o700)
        env = {key: value for key, value in os.environ.items()
               if not key.startswith(("RESTIC_", "RCLONE_"))}
        env.update({
            "PATH": str(tools) + os.pathsep + os.environ["PATH"],
            "XDG_CONFIG_HOME": str(root / "config"), "XDG_STATE_HOME": str(root / "state"),
            "XDG_DATA_HOME": str(root / "data"), "XDG_CACHE_HOME": str(root / "cache"),
            "XDG_RUNTIME_DIR": str(root / "runtime"), "TERM": "xterm-256color",
        })
        Path(env["XDG_RUNTIME_DIR"]).mkdir()

        def run(*args, expected=0):
            result = subprocess.run([BINARY, *args], env=env, cwd=workspace,
                                    stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=120)
            assert result.returncode == expected, (args, result.returncode, result.stdout, result.stderr)
            return result.stdout + result.stderr

        notes = workspace / "notes.txt"
        notes.write_text("original\n")
        folder = workspace / "folder"
        folder.mkdir()
        folder.joinpath("readme.md").write_text("saved folder\n")
        run("init", str(root / "repository"), "--path", str(workspace), "-y")
        run("snapshot")
        saved = json.loads(run("history", "notes.txt", "--json"))[0]["snapshot"]
        notes.write_text("accidental edit\n")

        refusal = run("restore", "notes.txt", expected=2)
        assert "rerun with -y" in refusal and notes.read_text() == "accidental edit\n"
        code, _ = interactive(["restore", "notes.txt"], env, workspace, b"n\r")
        assert code == 1 and notes.read_text() == "accidental edit\n", "Cancel changed the file"
        code, output = interactive(["restore", "notes.txt"], env, workspace, b"y\r")
        assert code == 0 and notes.read_text() == "original\n", "Confirmed restore failed"
        undo = re.search(r"--at ([0-9a-f]{8})", output)
        assert undo, "Restore must print an undo snapshot"
        run("restore", "notes.txt", "--at", undo[1], "-y")
        assert notes.read_text() == "accidental edit\n", "Undo did not preserve the current version"
        run("restore", "-y", "notes.txt", "--at", saved)
        assert notes.read_text() == "original\n"
        notes.unlink()
        run("restore", "notes.txt", "--at", saved, "-y")
        assert notes.read_text() == "original\n", "Deleted file was not recreated"
        copy = root / "copy.txt"
        run("restore", "notes.txt", "--at", saved, "--to", str(copy))
        assert copy.read_text() == "original\n"
        folder.joinpath("readme.md").write_text("changed folder\n")
        folder.joinpath("new.txt").write_text("keep me\n")
        run("restore", "folder", "--at", saved, "-y")
        assert folder.joinpath("readme.md").read_text() == "saved folder\n"
        assert folder.joinpath("new.txt").read_text() == "keep me\n"
        print("Passed: relative-file restore, confirmation/cancel, non-TTY refusal, -y, undo, deleted file, --to, and folder restore.")

        if shutil.which("rclone") or os.environ.get("CAIRN_RCLONE_TEST"):
            config = root / "config/rclone"
            config.mkdir(parents=True)
            config.joinpath("rclone.conf").write_text("[demo]\ntype = local\n")
            run("init", f"demo:{root / 'rclone-repository'}", "--path", str(workspace), "--password", "-y")
            run("snapshot")
            notes.write_text("remote adapter edit\n")
            run("restore", "notes.txt", "-y")
            assert notes.read_text() == "original\n"
            print("Passed: real rclone local adapter init, encrypted backup, and restore. No cloud account or NAS was used.")
        else:
            print("rclone adapter not exercised: install rclone or set CAIRN_RCLONE_TEST=1 to allow its pinned download.")


if __name__ == "__main__":
    main()
