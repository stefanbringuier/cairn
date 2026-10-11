"""Run the static binary in disposable Linux containers without network access."""

from pathlib import Path
import subprocess
import sys

images = sys.argv[2:] or ["debian:12", "ubuntu:24.04", "fedora:44", "archlinux:base", "alpine:3.23"]
binary = Path(sys.argv[1]).resolve()
for image in images:
    for args, expected in [("--version", "cairn "), ("help restore", "--at"), ("targets", "Google Drive")]:
        command = ["docker", "run", "--rm", "--network", "none", "--read-only",
                   "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", "65534:65534",
                   "--volume", f"{binary}:/usr/local/bin/cairn:ro", image, "/usr/local/bin/cairn", *args.split()]
        output = subprocess.check_output(command, text=True, timeout=180)
        assert expected in output, f"{image}: unexpected {args} output"
    print(f"Passed CLI startup, restore help, and targets: {image}")
