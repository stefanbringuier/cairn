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
            "TMPDIR": str(root / "tmp"),
        })
        Path(env["XDG_RUNTIME_DIR"]).mkdir()
        Path(env["TMPDIR"]).mkdir()

        def run(*args, expected=0):
            result = subprocess.run([BINARY, *args], env=env, cwd=workspace,
                                    stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=120)
            assert result.returncode == expected, (args, result.returncode, result.stdout, result.stderr)
            return result.stdout + result.stderr

        notes = workspace / "notes.txt"
        notes.write_text("original\n")
        notes.chmod(0o750)
        os.utime(notes, (1700000000, 1700000000))
        metadata = workspace / "metadata.txt"
        metadata.write_text("same bytes\n")
        metadata.chmod(0o600)
        link = workspace / "notes-link"
        link.symlink_to("notes.txt")
        folder = workspace / "folder"
        folder.mkdir()
        folder.joinpath("readme.md").write_text("saved folder\n")
        folder.chmod(0o750)
        os.utime(folder, (1700000000, 1700000000))
        run("init", str(root / "repository"), "--path", str(workspace), "-y")
        # Keep fixture versions that intentionally share one hourly retention bucket.
        run("set", "snapshot.keep", "100,24h,7d,4w")
        run("snapshot")
        saved = json.loads(run("history", "notes.txt", "--json"))[0]["snapshot"]
        assert len(saved) == 64, "History must expose an unambiguous full snapshot ID"
        identical = json.loads(run("diff", saved, saved, "--json"))
        assert identical["changes"] == [] and identical["from"] == saved
        notes.write_text("accidental edit\n")

        recovered = json.loads(run("recover", "notes.txt", "--at", saved, "--json"))
        assert recovered["snapshot"] == saved
        assert Path(recovered["destination"]).read_text() == "original\n"
        assert notes.read_text() == "accidental edit\n", "Recover changed the live file"
        assert Path(recovered["destination"]).stat().st_mode & 0o777 == 0o600
        run("recover", "notes.txt", "-y", expected=2)
        run("recover", "notes.txt", "-y=false", expected=2)
        for existing in (notes, folder, link):
            run("recover", "notes.txt", "--at", saved, "--to", str(existing), expected=1)
        assert notes.read_text() == "accidental edit\n" and link.is_symlink()
        folder_copy = root / "folder-copy"
        run("recover", "folder", "--at", saved, "--to", str(folder_copy))
        assert folder_copy.joinpath("readme.md").read_text() == "saved folder\n"

        refusal = run("restore", "notes.txt", expected=2)
        assert "rerun with -y" in refusal and notes.read_text() == "accidental edit\n"
        code, _ = interactive(["restore", "notes.txt"], env, workspace, b"n\r")
        assert code == 1 and notes.read_text() == "accidental edit\n", "Cancel changed the file"
        code, output = interactive(["restore", "notes.txt"], env, workspace, b"y\r")
        assert code == 0 and notes.read_text() == "original\n", "Confirmed restore failed"
        assert notes.stat().st_mode & 0o777 == 0o750 and int(notes.stat().st_mtime) == 1700000000
        undo = re.search(r"--at ([0-9a-f]{64})", output)
        assert undo, "Restore must print an undo snapshot"
        run("restore", "notes.txt", "--at", undo[1], "-y")
        assert notes.read_text() == "accidental edit\n", "Undo did not preserve the current version"
        run("restore", "-y", "notes.txt", "--at", saved)
        assert notes.read_text() == "original\n"
        notes.unlink()
        run("restore", "notes.txt", "--at", saved, "-y")
        assert notes.read_text() == "original\n", "Deleted file was not recreated"
        assert notes.stat().st_mode & 0o777 == 0o750 and int(notes.stat().st_mtime) == 1700000000
        link.unlink()
        run("restore", "notes-link", "--at", saved, "-y")
        assert link.is_symlink() and os.readlink(link) == "notes.txt", "Deleted symlink was not recreated"
        copy = root / "copy.txt"
        run("restore", "notes.txt", "--at", saved, "--to", str(copy))
        assert copy.read_text() == "original\n"
        container = root / "alternate-container"
        container.mkdir()
        container_notes = container / "notes.txt"
        container_notes.write_text("different current bytes\n")
        container_notes.chmod(0o600)
        run("restore", "notes.txt", "--at", saved, "--to", str(container), "-y")
        assert container_notes.read_text() == "original\n"
        assert container_notes.stat().st_mode & 0o777 == 0o750 and int(container_notes.stat().st_mtime) == 1700000000
        copy.write_text("recent alternate work\n")
        alternate = run("restore", "notes.txt", "--at", saved, "--to", str(copy), "-y")
        alternate_undo = re.search(r"--at ([0-9a-f]{64})", alternate)
        assert alternate_undo and copy.read_text() == "original\n"
        recovered_alt = root / "alternate-before-restore"
        run("recover", str(copy), "--at", alternate_undo[1], "--to", str(recovered_alt))
        assert recovered_alt.read_text() == "recent alternate work\n", "--to overwrite was not protected"
        run("set", "exclude", "+notes.txt")
        notes.write_text("excluded recent work\n")
        excluded = run("restore", "notes.txt", "--at", saved, "-y")
        excluded_undo = re.search(r"--at ([0-9a-f]{64})", excluded)
        run("recover", "notes.txt", "--at", excluded_undo[1], "--to", str(root / "excluded-undo"))
        assert root.joinpath("excluded-undo").read_text() == "excluded recent work\n"
        run("set", "exclude", "-notes.txt")
        folder.joinpath("readme.md").write_text("changed folder\n")
        folder.joinpath("new.txt").write_text("keep me\n")
        cached = folder / ".cache/work.bin"
        cached.parent.mkdir()
        cached.write_bytes(b"unsnapshotted cached work")
        folder_restored = run("restore", "folder", "--at", saved, "-y")
        assert folder.joinpath("readme.md").read_text() == "saved folder\n"
        assert folder.joinpath("new.txt").read_text() == "keep me\n"
        folder_undo = re.search(r"--at ([0-9a-f]{64})", folder_restored)
        run("recover", str(cached), "--at", folder_undo[1], "--to", str(root / "cached-undo"))
        assert root.joinpath("cached-undo").read_bytes() == b"unsnapshotted cached work"
        shutil.rmtree(folder)
        run("restore", "folder", "--at", saved, "-y")
        assert folder.joinpath("readme.md").read_text() == "saved folder\n"
        assert folder.stat().st_mode & 0o777 == 0o750 and int(folder.stat().st_mtime) == 1700000000
        forbidden = root / "repository/unexpected-output"
        run("restore", "notes.txt", "--at", saved, "--to", str(forbidden), "-y", expected=1)
        assert not forbidden.exists(), "Restore wrote inside its active repository"
        print("Passed: human confirmation/cancel, -y, complete undo including --to/excluded files, file metadata, deleted symlink, folder restore, and no-overwrite recovery.")

        if os.geteuid() != 0:
            notes.write_text("unreadable recent work\n")
            notes.chmod(0)
            try:
                partial = run("restore", "notes.txt", "--at", saved, "-y", expected=3)
                assert "nothing restored" in partial
            finally:
                notes.chmod(0o750)
            assert notes.read_text() == "unreadable recent work\n", "Partial safety backup allowed overwrite"
            notes.write_text("original\n")

        notes.write_text("new saved bytes\n")
        workspace.joinpath("notes.txt.old").write_text("boundary check\n")
        metadata.chmod(0o640)
        run("snapshot")
        newer = json.loads(run("history", "notes.txt", "--json"))[0]["snapshot"]
        filtered = json.loads(run("diff", saved, newer, "--path", "notes.txt", "--json"))
        assert filtered["summary"]["modified"] == 1 and len(filtered["changes"]) == 1
        assert filtered["changes"][0]["path"] == str(notes)
        ordinary_meta = json.loads(run("diff", saved, newer, "--path", str(metadata), "--json"))
        extra_meta = json.loads(run("diff", "--metadata", saved, newer, "--path", str(metadata), "--json"))
        assert ordinary_meta["changes"] == [] and extra_meta["summary"]["metadata_changed"] == 1
        safety_diff = json.loads(run("diff", saved, excluded_undo[1], "--path", "notes.txt", "--json"))
        assert safety_diff["to"] == excluded_undo[1] and safety_diff["summary"]["modified"] == 1
        run("restore", "notes.txt", "--at", saved, "-y")
        print("Passed: complete-safety refusal, native JSON diffs, exact path filtering, metadata comparisons, and explicit safety snapshot selection.")

        if shutil.which("ffmpeg") and shutil.which("ffprobe"):
            video = workspace / "dog.mp4"

            def make_video(seconds):
                subprocess.run(["ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i",
                                "color=c=blue:s=16x16:r=1", "-t", str(seconds), "-c:v", "mpeg4", str(video)],
                               env=env, check=True, capture_output=True, timeout=30)

            def duration(path):
                result = subprocess.run(["ffprobe", "-v", "error", "-show_entries", "format=duration",
                                         "-of", "json", str(path)], env=env, check=True, capture_output=True,
                                        text=True, timeout=30)
                return float(json.loads(result.stdout)["format"]["duration"])

            make_video(50)
            run("snapshot")
            short_video = json.loads(run("history", "dog.mp4", "--json"))[0]["snapshot"]
            make_video(80)
            run("snapshot")
            video_diff = json.loads(run("diff", short_video, "--path", "dog.mp4", "--json"))
            assert video_diff["summary"]["modified"] == 1
            # Simulate a newer edit that has not been saved to a snapshot.
            make_video(90)
            candidate = json.loads(run("recover", "dog.mp4", "--at", short_video, "--json"))
            assert abs(duration(candidate["destination"]) - 50) < 0.1
            assert abs(duration(video) - 90) < 0.1, "Candidate inspection lost unsnapshotted edits"
            print("Passed: real MP4 history/diff/recover/ffprobe found a 50-second copy while preserving unsnapshotted 90-second work.")
        else:
            print("Optional media recovery check skipped: ffmpeg/ffprobe are not installed.")

        if shutil.which("rclone") or os.environ.get("CAIRN_RCLONE_TEST"):
            config = root / "config/rclone"
            config.mkdir(parents=True)
            config.joinpath("rclone.conf").write_text("[demo]\ntype = local\n")
            # Failed connection must preserve the active repository and timer files.
            cairn_config = root / "config/cairn/config.json"
            before = cairn_config.read_bytes()
            timers = root / "config/systemd/user"
            timers.mkdir(parents=True, exist_ok=True)
            timer = timers / "cairn-snapshot.timer"
            timer.write_text("existing timer fixture\n")
            rclone_before = config.joinpath("rclone.conf").read_bytes()
            for target in ("missing:backups", "rclone:missing:backups"):
                output = run("init", target, "--password", "-y", expected=1)
                assert "cairn cloud setup" in output, output
                assert cairn_config.read_bytes() == before
                assert timer.read_text() == "existing timer fixture\n"
                assert not root.joinpath("config/cairn/password").exists()
                assert config.joinpath("rclone.conf").read_bytes() == rclone_before
            # Use a real encrypted repository with a different password. This
            # fails promptly after rclone connects, without backend retry delays.
            protected = root / "protected-rclone-repository"
            fixture_password = root / "fixture-password"
            fixture_password.write_text("isolated integration repository password\n")
            fixture_password.chmod(0o600)
            subprocess.run(
                ["restic", "--repo", str(protected), "--password-file", str(fixture_password), "init"],
                env=env, cwd=workspace, check=True, capture_output=True, text=True, timeout=120,
            )
            output = run("init", f"demo:{protected}", "--password", "-y", expected=1)
            assert "wrong password" in output, output
            assert cairn_config.read_bytes() == before
            assert timer.read_text() == "existing timer fixture\n"
            assert not root.joinpath("config/cairn/password").exists()
            assert config.joinpath("rclone.conf").read_bytes() == rclone_before
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
