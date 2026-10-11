"""Exercise release versions against real svu in disposable Git repositories."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import textwrap

SCRIPT = Path(__file__).resolve().with_name("next-release.sh")
SVU = str(Path(sys.argv[1]).resolve())
WORKFLOW = SCRIPT.parent.parent / ".github" / "workflows" / "release.yml"

with tempfile.TemporaryDirectory(prefix="cairn-release-test-") as directory:
    root = Path(directory)
    environment = os.environ | {"XDG_CONFIG_HOME": str(root / "config")}
    environment["GIT_CONFIG_GLOBAL"] = os.devnull
    environment["GIT_CONFIG_NOSYSTEM"] = "1"
    # Keep fixtures independent of hooks, signing, credentials, and the host repo.
    for key in list(environment):
        if key in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE") or key.startswith("GIT_CONFIG_COUNT"):
            del environment[key]

    def git(repo, *args):
        return subprocess.check_output(
            ["git", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false",
             "-c", "user.name=Release fixture", "-c", "user.email=fixture@example.invalid", *args],
            cwd=repo, env=environment, text=True, stderr=subprocess.PIPE,
        ).strip()

    def next_version(repo):
        return subprocess.check_output(
            ["sh", SCRIPT, SVU], cwd=repo, env=environment, text=True,
        ).strip()

    cases = [
        (["docs: clarify installation"], "v0.1.1"),
        (["ci: gate releases"], "v0.1.1"),
        (["refactor: simplify recovery", "test: cover recovery", "chore: refresh tooling"], "v0.1.1"),
        (["fix: preserve an existing file"], "v0.1.1"),
        (["feat: compare snapshots", "docs: describe comparisons"], "v0.2.0"),
        (["feat!: change the command contract"], "v1.0.0"),
        (["fix: change a setting\n\nBREAKING CHANGE: the previous setting is removed"], "v1.0.0"),
    ]
    for index, (messages, expected) in enumerate(cases):
        repo = root / str(index)
        repo.mkdir()
        git(repo, "init", "--initial-branch=main")
        git(repo, "commit", "--allow-empty", "-m", "chore: initial fixture")
        git(repo, "tag", "v0.1.0")
        assert next_version(repo) == "", "A tagged commit must not release twice"
        for message in messages:
            git(repo, "commit", "--allow-empty", "-m", message)
        actual = next_version(repo)
        assert actual == expected, (messages, actual, expected)
        git(repo, "tag", actual)
        assert next_version(repo) == "", "Rerunning successful CI must not create another tag"

    # Run the workflow's actual gate against a real main branch and mocked API
    # conclusions. This catches publication after missing, failed, or stale CI.
    step = WORKFLOW.read_text().split("- name: Validate release commit or retry tag\n", 1)[1]
    gate = textwrap.dedent(step.split("        run: |\n", 1)[1].split("      - name:", 1)[0])
    repo = root / "gate"
    repo.mkdir()
    git(repo, "init", "--initial-branch=main")
    git(repo, "commit", "--allow-empty", "-m", "fix: gate fixture")
    tested = git(repo, "rev-parse", "HEAD")
    git(repo, "update-ref", "refs/remotes/origin/main", tested)
    git(repo, "tag", "v0.1.1")
    git(repo, "checkout", "-b", "unmerged")
    git(repo, "commit", "--allow-empty", "-m", "fix: unmerged fixture")
    git(repo, "tag", "v9.9.9")
    git(repo, "checkout", "main")
    git(repo, "commit", "--allow-empty", "-m", "fix: newer fixture")
    tip = git(repo, "rev-parse", "HEAD")
    git(repo, "update-ref", "refs/remotes/origin/main", tip)

    tools = root / "tools"
    tools.mkdir()
    real_git = subprocess.check_output(["sh", "-c", "command -v git"], text=True).strip()
    tools.joinpath("git").write_text(
        '#!/bin/sh\n[ "$1" = fetch ] && exit 0\nexec "$CAIRN_TEST_GIT" "$@"\n'
    )
    tools.joinpath("gh").write_text(
        '#!/bin/sh\nprintf "%s\\n" "$CAIRN_TEST_CI"\nexit "$CAIRN_TEST_API_STATUS"\n'
    )
    for tool in tools.iterdir():
        tool.chmod(0o755)

    def check_gate(conclusion, *, tag="", checkout=None, allowed=False, skipped=False, api_status=0):
        git(repo, "checkout", "--detach", checkout or tip)
        output = root / "outputs"
        output.write_text("")
        result = subprocess.run(
            ["bash", "-e", "-o", "pipefail", "-c", gate], cwd=repo,
            env=environment | {
                "PATH": str(tools) + os.pathsep + os.environ["PATH"],
                "CAIRN_TEST_GIT": real_git, "CAIRN_TEST_CI": conclusion,
                "CAIRN_TEST_API_STATUS": str(api_status), "GITHUB_REPOSITORY": "fixture/cairn",
                "GITHUB_OUTPUT": str(output), "RETRY_TAG": tag,
            }, capture_output=True, text=True,
        )
        assert ("ready=true" in output.read_text()) == allowed, (tag, conclusion, result.stdout, result.stderr)
        assert (result.returncode == 0) == (allowed or skipped), (tag, conclusion, result.stdout, result.stderr)

    check_gate("success", allowed=True)
    for conclusion in ("failure", "cancelled", "null", "", "timed_out"):
        check_gate(conclusion)
        check_gate(conclusion, tag="v0.1.1")
    check_gate("success", api_status=1)
    check_gate("success", checkout=tested, skipped=True)
    check_gate("success", tag="v0.1.1", allowed=True)
    check_gate("success", tag="v9.9.9")
    check_gate("success", tag="v8.8.8")
    check_gate("success", tag="../../main")

print("Release checks passed: patch/minor/major, reruns, failed or missing CI, stale main, API errors, and retry tag validation.")
