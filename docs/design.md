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
| `profiles.go` | Explicit profile selection, inventory, shared repository protection |
| `restic.go`, `restore.go`, `recover.go`, `diff.go`, `run.go` | Repository access, safe exports, snapshot comparisons, restore safety, backup runs |
| `fetch.go` | Pinned, checksum-verified dependency binaries |
| `rebuild.go` | Recovery and individually confirmed package installation |
| `tui.go`, `ui.go`, `guide.go` | Bubble Tea state, presentation, help and setup screens |
| `skill.md` | Embedded command contract for agents |

restic owns the repository format, deduplication, and transfer. rclone owns
remote configuration and authentication. systemd owns scheduling. The Go
program stays independent of the docs and screenshot tooling.

## Named profiles

One invocation selects one profile with the global prefix `--profile NAME`.
The implicit `default` profile retains legacy files and units in place; named
profiles use `profiles/NAME` beneath the same config and state roots. Selection
is never persisted. The TUI, restic password, settings, status, manifests,
progress, pruning marker, rebuild scratch and printed commands all use that
selection. When a path contains terminal controls, undo prints the profile and
snapshot ID instead of constructing a command from a sanitized path.
Dependency binaries remain shared.
Unqualified interactive dashboard startup checks for an absent default config
before any repository work. If named profiles exist, a separate Bubble Tea
startup chooser displays local validated configuration only (no repository or
scheduler queries, tool downloads or writes). After the chooser exits, re-read
the chosen configuration and set the invocation's selection before starting
the dashboard and its asynchronous work. This does not mutate selection while
repository commands are in flight. Explicit selection, non-TTY commands and
JSON retain strict default/profile behavior; malformed configs and dangling
default config symlinks cannot trigger fallback. Full switching inside a live
dashboard is separate work; the startup chooser does not provide that feature.
Each profile also selects machine or data-only scope. Data-only exclusions cover
all default/named manifests and orphaned state, preventing recipes from another
profile leaking into a HOME source. Rclone accounts and definitions remain in
normal shared rclone configuration; profile selection does not isolate them.

Timers use `cairn-NAME-{snapshot,backup}` for named profiles and the original
unit names for default. Their ExecStart explicitly selects the profile and
Environment preserves absolute XDG config, state, data and cache roots.
Validate all roots and schedules before writing units; reject characters
systemd would reinterpret. Inventory reports actual enabled state separately
from configured frequency, including unknown when systemd is unavailable.

Keep the existing state-root flock across all profiles. This deliberately
serializes backup jobs and their retention/pruning on one machine; independent
repositories still have separate progress and pruning state. Restic handles
locks from other clients. Refuse init on a repository already configured by
another profile, resolving local symlink aliases, because host/tag retention
would otherwise prune another profile's history. Provider aliases cannot be
resolved locally; document the need for distinct remote storage as well.

Discover every configured local repository for run exclusions and restore
overlap checks. Protect the entire cairn config root, including other passwords.
Follow config/password file symlinks outside that root and protect their targets
and containing destinations, preserving existing credential arrangements.
Invalid saved configurations abort discovery instead of silently omitting a
repository. Reject symlink profile directories. Use an atomic config-file
replacement and remove a new password on pre-save init failure. Existing
passwords and configurations remain intact. Reinitializing a profile retains
settings, warns and confirms repository changes, and leaves its old snapshots
in the old repository. These guards remain accident prevention, not permissions.

`cairn cloud setup` is a fixed `rclone config` invocation with inherited terminal
streams. It reuses `tool("rclone")` so first-time setup can download the pinned,
checksum-checked binary before any repository is connected. It accepts no
pass-through arguments and never changes cairn settings or timers. Rclone owns
its configuration, browser flow, credentials, and configuration environment.
Initialization checks configured remotes for shorthand and explicit rclone
targets. Failed cloud connections report setup guidance without backend error
text that could contain credentials. Configuration and scheduling begin only
after a successful connection; any new password is removed on earlier failure.

## Extend a command

Write its expected behavior in [spec.md](spec.md) first. Add dispatch and
command-specific help, keep JSON output structured, and define exit codes.
Use a small function that returns an error; the executable owns termination.
Add focused tests and update [usage.md](usage.md) and the agent reference.

Backup scope is persisted in `Config.BackupScope`, defaulting legacy configs to
machine scope. Init owns scope changes and requires explicit sources when
switching an existing configuration. Path edits never infer scope. Data-only
runs skip manifest collection and exclude cairn's manifest directory,
including stale recipes contained in a source. They retain the normal restic
job tags and add `data-only`; rebuild rejects data-only configurations and
filters that tag from both recipe and home selection after reconnecting.
Rebuild stages explicitly recorded manifests from its chosen machine backup
in a fresh scratch directory, even for sources under the old home. Package and
crontab steps read only that directory; export failure aborts those steps, and
stale live manifests cannot supplement missing managers in the chosen recipe.

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
not Google authorization or an actual NAS connection. Failed-connection checks
use an incorrect password against a disposable encrypted local repository, so
they exercise credential rollback without waiting for backend connection retries.
Integration fixtures resolve both system binaries and cairn's private downloaded
tools for direct commands. CI exercises automatic downloads without system restic
or rclone installations.

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
