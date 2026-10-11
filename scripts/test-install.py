"""Check installation with local release fixtures; never install on the host."""

import hashlib
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile

INSTALLER = Path(__file__).resolve().with_name("install.sh")

with tempfile.TemporaryDirectory(prefix="cairn-install-test-") as directory:
    root = Path(directory)
    tools, releases = root / "tools", root / "releases"
    tools.mkdir()
    releases.mkdir()
    tools.joinpath("uname").write_text(
        '#!/bin/sh\ncase "$1" in -s) printf "%s\\n" "$CAIRN_TEST_OS";; -m) printf "%s\\n" "$CAIRN_TEST_ARCH";; esac\n'
    )
    tools.joinpath("curl").write_text('''#!/usr/bin/env python3
import os, shutil, sys
from pathlib import Path
args = sys.argv[1:]
url = next(arg for arg in args if arg.startswith("https://"))
with open(os.environ["CAIRN_TEST_LOG"], "a") as log:
    log.write(url + "\\n")
shutil.copyfile(Path(os.environ["CAIRN_TEST_RELEASES"]) / url.rsplit("/", 1)[1], args[args.index("-o") + 1])
''')
    for tool in tools.iterdir():
        tool.chmod(0o755)
    executable = b"#!/bin/sh\nprintf 'cairn fixture\\n'\n"
    def publish(executable):
        checksums = []
        for arch in ("amd64", "arm64"):
            archive = releases / f"cairn_linux_{arch}.tar.gz"
            with tarfile.open(archive, "w:gz") as bundle:
                member = tarfile.TarInfo("cairn")
                member.mode, member.size = 0o755, len(executable)
                bundle.addfile(member, io.BytesIO(executable))
            checksums.append(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n")
        releases.joinpath("checksums.txt").write_text("".join(checksums))

    publish(executable)
    log = root / "downloads.txt"
    environment = {key: value for key, value in os.environ.items() if not key.startswith("CAIRN_")}
    environment.update({
        "PATH": str(tools) + os.pathsep + os.environ["PATH"],
        "TMPDIR": str(root), "CAIRN_TEST_OS": "Linux", "CAIRN_TEST_ARCH": "x86_64",
        "CAIRN_TEST_RELEASES": str(releases), "CAIRN_TEST_LOG": str(log),
        "CAIRN_INSTALL_DIR": str(root / "custom bin"),
        "XDG_CONFIG_HOME": str(root / "config"), "XDG_STATE_HOME": str(root / "state"),
    })

    def run(expected=0, **settings):
        result = subprocess.run(["sh", INSTALLER], env=environment | settings,
                                capture_output=True, text=True, timeout=15)
        assert (result.returncode == 0) == (expected == 0), (result.stdout, result.stderr)
        return result

    run()
    installed = Path(environment["CAIRN_INSTALL_DIR"]) / "cairn"
    assert installed.read_bytes() == executable and os.access(installed, os.X_OK)
    assert "/latest/download/cairn_linux_amd64.tar.gz" in log.read_text()
    run(CAIRN_TEST_ARCH="aarch64", CAIRN_VERSION="v0.1.0")
    assert "/download/v0.1.0/cairn_linux_arm64.tar.gz" in log.read_text()
    assert subprocess.check_output([installed], text=True).strip() == "cairn fixture"
    config = root / "config" / "cairn" / "config.json"
    config.parent.mkdir(parents=True)
    config.write_text('{"repo":"keep this backup destination"}\n')
    state = root / "state" / "cairn" / "last-run"
    state.parent.mkdir(parents=True)
    state.write_text("keep scheduler state\n")
    before = {path: path.read_bytes() for path in (config, state)}
    upgraded = b"#!/bin/sh\nprintf 'cairn newer fixture\\n'\n"
    publish(upgraded)
    run()
    assert installed.read_bytes() == upgraded
    assert subprocess.check_output([installed], text=True).strip() == "cairn newer fixture"
    assert all(path.read_bytes() == contents for path, contents in before.items())
    installed.write_text("keep the previous installation\n")
    releases.joinpath("checksums.txt").write_text(f"{'0' * 64}  cairn_linux_amd64.tar.gz\n")
    assert "checksum did not match" in run(expected=1).stderr
    assert installed.read_text() == "keep the previous installation\n"
    downloads = log.read_text()
    for settings in ({"CAIRN_TEST_OS": "Darwin"}, {"CAIRN_TEST_ARCH": "riscv64"}, {"CAIRN_VERSION": "../../main"}):
        run(expected=1, **settings)
        assert log.read_text() == downloads, "Unsupported inputs should not download anything"
    assert not any(path.name.startswith("tmp.") for path in root.iterdir()), "Installer left temporary downloads"
    print("Installer passed: amd64/arm64, updates preserving settings/state, version selection, paths with spaces, checksum rejection, unsupported inputs, and cleanup.")
