# Design and extension

cairn gives Linux users short commands for saving versions, getting files back,
and recovering a machine. Keep that scope small. A person and an agent should
be able to use the same commands and understand the same results.

## Boundaries

| Location | Responsibility |
|---|---|
| `cmd/cairn/main.go` | Build version and process exit |
| `internal/app/cli.go`, `help.go` | Command dispatch, prompts, examples, storage guides |
| `config.go`, `schedule.go` | User defaults, retention, systemd timers, status |
| `restic.go`, `restore.go`, `run.go` | Repository access, restore safety, backup runs |
| `fetch.go` | Pinned, checksum-verified dependency binaries |
| `rebuild.go` | Recovery and individually confirmed package installation |
| `tui.go`, `ui.go`, `guide.go` | Bubble Tea state, presentation, help and setup screens |
| `skill.md` | Embedded command contract for agents |

restic owns the repository format, deduplication, and transfer. rclone owns
remote configuration and authentication. systemd owns scheduling. The Go
program stays independent of the docs and screenshot tooling.

## Extend a command

Write its expected behavior in [SPEC.md](SPEC.md) first. Add dispatch and
command-specific help, keep JSON output structured, and define exit codes.
Use a small function that returns an error; the executable owns termination.
Add focused tests and update [usage.md](usage.md) and the agent reference.

Relative file paths resolve against the current working directory. A deleted
file does not have to exist for restoration. `-y` accepts confirmation but
does not remove the pre-restore snapshot. Changes to this flow need the real
disposable-repository integration check.

## Extend the TUI

Add a screen and its transitions in `tui.go`. Keep process and repository work
in Bubble Tea commands so a slow destination cannot block input. Reuse `ui.go`
for colors, panels, the stone mark, badges, and keys. List views paginate;
long guides scroll. Keep navigation and explanations available by keyboard.

The dashboard distinguishes snapshots from full backups. `?` explains the
basics; `t` opens destination guides. Selecting a destination shows setup
commands; it does not switch or connect the repository. Settings edit schedules
and retention. Restores show changes and preserve the current version first.

Test long paths, 40–110 columns, and 24-row terminals. Reserve space for the
header and wrapped key hints. Refresh screenshots with the running binary;
use disposable files and clearly describe fixture metadata.

## Extend storage

Prefer an existing restic URL or writable rclone backend. Add destination
examples to the shared `storageChoices` data, user guide, and specification.
Keep source paths local: mount or sync remote source data first. Explicitly add
mounted sources because runs use `--one-file-system`.

Use `--password` for cloud and shared destinations. Scheduled processes need
credentials available to the systemd user manager. Never use a contributor's
real repository for tests. A local rclone adapter check proves the adapter,
not Google authorization or an actual NAS connection.

## Linux, releases, and docs

Release binaries use `CGO_ENABLED=0` for Linux amd64 and arm64, avoiding a
distribution-specific libc dependency. Scheduling requires systemd user timers
and linger support; package recovery uses only the managers available locally.
Container smoke checks prove binary startup, not systemd or a complete recovery.

CI checks formatting, lint, race tests, dependency downloads, documentation
coverage, static cross-builds, and startup on several Linux distributions.
The release workflow waits for successful main-branch CI before creating a
Conventional Commit-driven version tag and publishing checked archives.

The docs site renders the repository's Markdown and images with a small static
builder. CI validates local links and builds the site. Pages deployment is
disabled while private and additionally requires `PAGES_ENABLED=true`. See
[publishing.md](publishing.md) for the maintainer's activation steps.
