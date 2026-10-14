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


def data_only_checks(root, tools, base_env):
    """Use an isolated home and real restic to verify scope and mode transitions."""
    home = root / "data-home"
    home.mkdir()
    home.joinpath("notes.txt").write_text("data-only bytes\n")
    extra = root / "extra-data"
    extra.mkdir()
    extra.joinpath("second.txt").write_text("second source\n")
    env = {**base_env, "HOME": str(home),
           "XDG_CONFIG_HOME": str(home / ".config"),
           "XDG_STATE_HOME": str(home / ".local/state"),
           "XDG_DATA_HOME": str(home / ".local/share"),
           "XDG_CACHE_HOME": str(home / ".cache")}
    repo = root / "data-repository"
    config = home / ".config/cairn/config.json"
    manifests = home / ".local/state/cairn/manifests"
    manifests.mkdir(parents=True)
    stale = manifests / "stale.txt"
    stale.write_text("do not ship an old machine recipe\n")

    def run(*args, expected=0):
        result = subprocess.run([BINARY, *args], env=env, cwd=home,
                                stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=120)
        assert result.returncode == expected, (args, result.returncode, result.stdout, result.stderr)
        return result.stdout + result.stderr

    def restic(*args):
        result = subprocess.run(["restic", "--repo", str(repo), "--insecure-no-password", *args],
                                env=env, capture_output=True, text=True, timeout=120)
        assert result.returncode == 0, (args, result.stdout, result.stderr)
        return result.stdout

    def saved():
        return json.loads(restic("snapshots", "--json"))

    def assert_data_versions(paths):
        for version in saved():
            assert sorted(version["paths"]) == sorted(map(str, paths)), version
            if "backup" in version["tags"]:
                assert "data-only" in version["tags"], version
            else:
                assert version["tags"] == ["snapshot"], version
            nodes = [json.loads(line) for line in restic("ls", "--json", version["id"]).splitlines()]
            assert not any(node.get("path", "").startswith(str(manifests)) for node in nodes), nodes
            assert any(node.get("path") == str(home / "notes.txt") for node in nodes), nodes

    # Without --path a new data-only configuration defaults both jobs to HOME.
    output = run("init", str(repo), "--data-only", "-y")
    c = json.loads(config.read_text())
    assert c["backup_scope"] == "data-only" and "backup.scope" in output
    assert c["snapshot"]["paths"] == c["backup"]["paths"] == [str(home)]
    run("snapshot")
    run("backup")
    assert_data_versions([home])
    assert list(manifests.iterdir()) == [stale], "Data-only run generated manifests"
    assert "data-only" in run("rebuild", "-y", expected=1)
    assert json.loads(run("status", "--json"))["backup_scope"] == "data-only"

    # Settings are saved before the deliberately unavailable scheduler fails.
    run("set", "snapshot.every", "off", expected=1)
    run("set", "backup.every", "off", expected=1)
    run("set", "snapshot.keep", "100,24h")
    run("set", "backup.keep", "100,7d")
    run("set", "exclude", "+*.skip")
    before = json.loads(config.read_text())
    run("init", str(repo), "-y")
    after = json.loads(config.read_text())
    for key in ("backup_scope", "snapshot", "backup", "exclude"):
        assert before[key] == after[key], (key, before, after)
    run("init", str(repo), "--path", str(home), "--path", str(extra), "-y")
    c = json.loads(config.read_text())
    assert c["snapshot"]["paths"] == c["backup"]["paths"] == [str(home), str(extra)]
    run("snapshot")
    run("backup")
    for version in saved():
        assert "/etc" not in version["paths"] and "/usr/share/keyrings" not in version["paths"]

    before = config.read_bytes()
    rejected_target = root / "scope-rejected-repository"
    run("init", str(rejected_target), "--data-only=false", "-y", expected=2)
    assert config.read_bytes() == before and not rejected_target.exists(), "Rejected transition had side effects"
    run("init", str(repo), "--data-only=false", "--path", str(home), "-y")
    c = json.loads(config.read_text())
    assert c["backup_scope"] == "machine"
    assert c["snapshot"]["paths"] == [str(home)]
    assert c["backup"]["paths"] == [str(home), "/etc", "/usr/share/keyrings"]
    assert "no backups" in run("rebuild", "--from", str(home), expected=1), "Rebuild selected a data-only recipe"

    # Collect fixture manifests, not package lists from the test runner's account.
    manager_log = root / "scope-manager-calls"
    rebuild_log = root / "scope-rebuild-calls"
    env["CAIRN_TEST_MANIFEST_LOG"] = str(manager_log)
    env["CAIRN_TEST_REBUILD_LOG"] = str(rebuild_log)
    for name in ("apt-mark", "snap", "flatpak", "brew", "uv", "pipx", "cargo", "npm", "crontab"):
        tools.joinpath(name).write_text(
            '#!/bin/sh\nprintf "called\\n" >> "$CAIRN_TEST_MANIFEST_LOG"\n'
            + f'printf "{name} %s\\n" "$*" >> "$CAIRN_TEST_REBUILD_LOG"\n'
            + ('exit 1\n' if name == "npm" else 'printf "demo\\n"\n'))
        tools.joinpath(name).chmod(0o700)
    run("set", "backup.paths", str(home))  # keep all machine sources disposable
    run("backup")
    machine = saved()[-1]
    assert machine["tags"] == ["backup"] and str(manifests) in machine["paths"]
    calls = manager_log.read_bytes()
    assert manifests.joinpath("apt.txt").read_text() == "demo\n"

    before = config.read_bytes()
    run("init", str(rejected_target), "--data-only", "-y", expected=2)
    assert config.read_bytes() == before and not rejected_target.exists()
    run("init", str(repo), "--data-only", "--path", str(home), "-y")
    run("backup")
    data = saved()[-1]
    assert data["tags"] == ["backup", "data-only"] and data["paths"] == [str(home)]
    assert manager_log.read_bytes() == calls, "Data-only run invoked package managers"
    nodes = [json.loads(line) for line in restic("ls", "--json", data["id"]).splitlines()]
    assert not any(node.get("path", "").startswith(str(manifests)) for node in nodes)
    run("init", str(repo), "--data-only=false", "--path", str(home), "-y")
    refusal = run("rebuild", "--from", str(home), expected=2)
    assert machine["short_id"] in refusal and data["short_id"] not in refusal, refusal
    c = json.loads(config.read_text())
    assert c["snapshot"]["every"] == c["backup"]["every"] == "off"
    assert c["snapshot"]["keep"] == "100,24h" and c["backup"]["keep"] == "100,7d"
    assert c["exclude"] == ["*.skip"]

    # Run the real rebuild through HOME/recipe restore, with all installs inert.
    # An absent recipe manager must not pick up a stale live list left in HOME.
    manifests.joinpath("apt.txt").write_text("stale-live-package\n")
    manifests.joinpath("npm.txt").write_text("stale-extra-manager\n")
    for name, script in {
        "sudo": 'printf "sudo %s\\n" "$*" >> "$CAIRN_TEST_REBUILD_LOG"\n',
        "apt-cache": 'printf "demo\\n"\n',
    }.items():
        tools.joinpath(name).write_text("#!/bin/sh\n" + script)
        tools.joinpath(name).chmod(0o700)
    rebuild_log.write_text("")
    run("rebuild", "--from", str(home), "-y")
    installed = rebuild_log.read_text()
    assert "sudo apt install -y demo" in installed, installed
    assert "npm " not in installed and "stale-" not in installed, installed
    crontab = [line for line in installed.splitlines() if line.startswith("crontab ")]
    assert len(crontab) == 1 and "/rebuild/manifests-" in crontab[0], installed
    assert not list(home.joinpath(".local/state/cairn/rebuild").glob("manifests-*")), "Recipe scratch leaked"
    print("Passed: data-only sources/tags, stale-manifest exclusion, default machine compatibility, preserved settings, explicit scope transitions, and rebuild refusal/recipe filtering.")


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
        defaults = json.loads(run("set", "--json"))
        assert defaults["backup_scope"] == "machine"
        assert defaults["snapshot"]["paths"] == [str(workspace)]
        assert defaults["backup"]["paths"] == [str(workspace), "/etc", "/usr/share/keyrings"]
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
            run("init", f"demo:{root / 'rclone-repository'}", "--path", str(workspace), "--password", "-y")
            run("snapshot")
            notes.write_text("remote adapter edit\n")
            run("restore", "notes.txt", "-y")
            assert notes.read_text() == "original\n"
            print("Passed: real rclone local adapter init, encrypted backup, and restore. No cloud account or NAS was used.")
        else:
            print("rclone adapter not exercised: install rclone or set CAIRN_RCLONE_TEST=1 to allow its pinned download.")

        data_only_checks(root, tools, env)


if __name__ == "__main__":
    main()
