# Contributing

Use the Go version in [go.mod](go.mod). Run these commands from the repository root:

```sh
make setup    # download checksum-verified golangci-lint; enable local Git hooks
make fmt      # apply gofumpt and goimports
make check    # format, lint, race tests, go mod tidy -diff, build
make vuln     # the same pinned govulncheck used in CI
```

Tools live in the ignored `.cache/bin/` directory. `make setup` needs curl and tar. CI uses the same golangci-lint version and formatting configuration. Editors inherit whitespace settings from [.editorconfig](.editorconfig).

## Design belongs in the PR

Read [docs/spec.md](docs/spec.md), [docs/design.md](docs/design.md), and the root
[AGENTS.md](AGENTS.md) before extending cairn. Every PR includes the relevant
`docs/` update. Behavior and architecture changes update the specification and
design notes in the same PR, so reviewers can assess the intended behavior
alongside the code. CI requires specification or design changes when production
Go files change, and a `docs/*.md` update in every PR.

Commands and flags need examples in [docs/usage.md](docs/usage.md) and
command-specific CLI help. Keep [internal/app/skill.md](internal/app/skill.md)
in sync for agents. Visible TUI changes include captures from the actual app.
Use the PR template to link the updated sections and report validation.

Keep cairn thin: use restic for repositories, rclone for remotes, and systemd
for scheduling. Add a small command or screen rather than another abstraction
unless it creates a clear boundary. Reuse the palette, mark, panels, and key
hints. Explain actions in basic terms, preserve keyboard navigation, and check
24-row terminals and long paths. [docs/design.md](docs/design.md) describes the
extension points and restore safety contract.

## Layout

```text
cmd/cairn/             executable entry point and release version
internal/app/          commands, backup logic, TUI, and colocated tests
internal/app/skill.md  embedded reference printed by cairn --skill
docs/                 usage guide and README images
scripts/              development tooling and screenshot capture
.githooks/             versioned Git hooks
```

Keep implementation packages internal until they need a supported public API. The `app.Run` boundary returns an exit status; the executable owns process termination.

`make build` writes `bin/cairn`; `make install` installs to Go's configured binary directory. Source installs use `github.com/stefanbringuier/cairn/cmd/cairn`, and GoReleaser builds that same command. Set `VERSION=1.2.3` to exercise release version injection.

## Hooks & commits

The pre-commit hook checks whitespace and exports the **staged tree** to a temporary directory, then runs `make check`. It handles partial staging without changing your working files. The commit-msg hook requires a Conventional Commit subject:

```text
feat: add a backup target
fix: handle an offline repository
refactor: simplify restore selection
```

Create a branch and open a PR to `main`. Use your own author identity and a
Conventional Commit PR title: GitHub squash-merges the PR under that title,
preserving the contributor as author and using GitHub as committer. Main CI
accepts the maintainer or GitHub as committer. Do not add attribution trailers
automatically.

Required CI checks must pass against the current main branch before merging.
Main CI runs again after the merge; only a successful run for the current main
tip publishes a release. Rapid merges can combine into one release; commits
from superseded runs enter the next passing tip's version calculation.
Patch is the default, including documentation and CI changes.
`feat:` selects a minor version; `feat!:` or a `BREAKING CHANGE:` footer
selects a major version. Put a breaking-change footer in the PR body so it is
included in the squash commit. PR CI validates the title and body against the
main commit rules before merging. Merging is a maintainer action because it starts
publication. Details and retry instructions are in
[docs/publishing.md](docs/publishing.md).
Editing a PR title or body reruns CI to validate the new squash commit metadata.

## Verify behavior and portability

`make check vet` is the normal Go check. Use `make integration` for restore or
storage changes (Python 3, plus restic 0.17+ on PATH or network access for cairn's
pinned download). It checks a relative file,
interactive confirmation and cancellation, noninteractive refusal, `-y`, a
deleted file, the undo snapshot, and a directory. If rclone is installed it
also exercises a real local rclone remote, without touching your accounts.
Set `CAIRN_RCLONE_TEST=1` to exercise the adapter with cairn's pinned download
when rclone is not installed; CI tests that automatic bootstrap path.
It also checks snapshot diffs, no-overwrite recovery, permissions/timestamps,
named-profile selection, independent credentials/state/timers, generated timer
commands with isolated XDG roots, and unchanged legacy configuration/units,
deleted links, incomplete safety backups, and undo for normally excluded files
and alternate destinations. If ffmpeg and ffprobe are installed, it recovers a
real 50-second video while preserving a newer unsnapshotted edit.
It also verifies data-only snapshots and backups, stale-manifest exclusion,
scope changes with preserved settings, and rebuild recipe filtering.

Review [agent safety](docs/agent-safety.md) before changing recovery behavior.
Keep agent defaults on separate copies; permission enforcement belongs to the
operating system or a trusted executor, not a bypassable command flag.

`make cross-check` builds static Linux amd64 and arm64 binaries. CI exercises
startup in Debian, Ubuntu, Fedora, Arch, and Alpine containers; this is binary
compatibility, not systemd scheduling. Native arm64 tests become available when
the repo is public. Scheduling requires systemd user timers and linger.

## Screenshots

Install Node.js, Python 3, and restic 0.17+ for capture. Browser tooling stays in `scripts/screenshots/`, separate from the Go module:

```sh
npm ci --prefix scripts/screenshots
cd scripts/screenshots
npx playwright install chromium
cd ../..
make screenshots
```

The script starts the actual `bin/cairn` in a PTY, renders its ANSI output in xterm.js, and uses Playwright to capture the dashboard, restore flow, settings, storage guides, help, and CLI confirmation. All files, snapshots, configuration, and state live in a temporary directory. Scheduler metadata is a fixture; host timers are never accessed. Restores are canceled. Storage screens are setup examples, not connected provider accounts.

Images go to `docs/images/`. Set `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` to use an existing Chromium binary. Screenshot tooling is optional for normal Go development.

## Documentation site

The site uses the same Markdown and images as the repository, including this
guide and AGENTS.md. There is one optional, hash-pinned Python dependency and
no site dependency in the Go module.

```sh
make docs-tools   # install into .cache/docs-env; needs Python 3.11+
make docs         # render site/ and validate links and anchors
make docs-serve   # preview at http://127.0.0.1:8000
```

The build fails on broken local links. Edit Markdown in place and the small
theme in `scripts/docs/`; do not commit generated `site/` files. Pages publishes
only from public `main` with `PAGES_ENABLED=true`. See
[docs/publishing.md](docs/publishing.md) before activating it.

With the optional screenshot tools installed, run
`node scripts/screenshots/docs.mjs` after `make docs` to check desktop and
mobile layouts, search, and navigation beneath the `/cairn/` project path.
Preview images are written to `/tmp/cairn-docs-desktop.png` and
`/tmp/cairn-docs-mobile.png`.

To verify the pinned restic download as well:

```sh
CAIRN_NET_TEST=1 go test -run TestFetchRestic ./...
```
