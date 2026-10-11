"""Require design documentation alongside code in a pull request."""

from pathlib import PurePosixPath
import subprocess
import sys


def check(files):
    docs = {file for file in files if file.startswith("docs/") and file.endswith(".md")}
    production = any(file.endswith(".go") and not file.endswith("_test.go") for file in files)
    if files and not docs:
        return "Every PR must include a relevant docs/*.md update."
    if production and not docs.intersection({"docs/SPEC.md", "docs/design.md"}):
        return "Production Go changes must update docs/SPEC.md or docs/design.md (both when behavior changes)."
    if any(set(PurePosixPath(file).parts).intersection({".cursor", ".codex", ".claude", ".agents"}) for file in files):
        return "Use root AGENTS.md; do not add vendor-specific agent dotfolders."
    return None


if __name__ == "__main__":
    base, head = sys.argv[1:]
    # Deletions cannot satisfy the documentation requirement.
    files = subprocess.check_output(
        ["git", "diff", "--name-only", "--diff-filter=ACMRT", f"{base}...{head}"], text=True
    ).splitlines()
    if error := check(files):
        sys.exit(error)
    print("PR documentation coverage passed.")
