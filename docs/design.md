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
| `restic.go`, `restore.go`, `recover.go`, `diff.go`, `run.go` | Repository access, safe exports, snapshot comparisons, restore safety, backup runs |
| `fetch.go` | Pinned, checksum-verified dependency binaries |
| `rebuild.go` | Recovery and individually confirmed package installation |
| `tui.go`, `ui.go`, `guide.go` | Bubble Tea state, presentation, help and setup screens |
| `skill.md` | Embedded command contract for agents |

restic owns the repository format, deduplication, and transfer. rclone owns
remote configuration and authentication. systemd owns scheduling. The Go
program stays independent of the docs and screenshot tooling.

## Extend a command

Write its expected behavior in [spec.md](spec.md) first. Add dispatch and
command-specific help, keep JSON output structured, and define exit codes.
Use a small function that returns an error; the executable owns termination.
Add focused tests and update [usage.md](usage.md) and the agent reference.

Relative file paths resolve against the current working directory. A deleted
file does not have to exist for restoration. `-y` accepts confirmation but
does not remove the pre-restore snapshot. Changes to this flow need the real
disposable-repository integration check.

## Agents and permission boundaries

Expose restic's snapshot diff as a small, read-only command with path filtering
and one JSON object. Keep media interpretation outside cairn: agents can
recover candidate videos and run ffprobe on the copies. A byte-level diff
cannot establish a video's duration or explain a scene change.

`recover` is the default action for agents. It writes a new file or folder
through staging and `RENAME_NOREPLACE`, so even a concurrent destination
creation cannot turn an export into an overwrite. `restore` remains the
human-facing replacement operation; complete safety backups are mandatory for
existing destinations, including alternate destinations.

CLI guards prevent accidental damage through these commands. They cannot
restrict an agent with the same Linux permissions as the maintainer. An
unrestricted shell can invoke restic, change config, or write files directly.
For enforced access, use a separate Linux identity or an executor outside the
agent: allow only history/diff/recover, supply read-only repository credentials
and read-only config, grant write access only to a separate recovery directory,
and withhold sudo and maintainer credentials. Restrict access to the intended
project's repository rather than exposing unrelated backups. A trusted human
or broker performs approved replacements; a prompt in a terminal that the
agent controls is not independent approval. See [agent-safety.md](agent-safety.md).

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
Changes enter `main` through PRs with required, current CI checks. The release
workflow then waits for successful main-branch CI, including the docs build,
before creating a version tag and publishing checked archives. Every new
current passing tip releases a patch by default; Conventional Commit features and
breaking changes select minor and major versions. A tagged commit does not
release again on a rerun. Manual publication retries need an existing main tag
and evidence that its commit passed main CI. Main protection settings live in
`.github/main-protection.json` and are applied by `scripts/protect-main.sh`.

`scripts/install.sh` keeps installation separate from the Go app. It detects
the release architecture, verifies the archive checksum, and installs a user
binary. Rerunning it updates that binary without changing settings or backups;
updates do not run in the background. README and usage examples separate
installation from choosing a backup
destination. CI checks the installer with isolated local release fixtures.
The docs theme adds copy controls to shell command blocks while
keeping the original Markdown usable on GitHub.

The docs site renders the repository's Markdown and images with a small static
builder. CI validates local links and builds the site. Pages deployment is
disabled while private and additionally requires `PAGES_ENABLED=true`. See
[publishing.md](publishing.md) for the maintainer's activation steps.
