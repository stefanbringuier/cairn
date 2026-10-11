# cairn specification

This is the behavior contract for cairn. PRs update it alongside changes to the
implementation; architecture and extension points live in [design.md](design.md).

cairn is a thin CLI + TUI over restic. restic stores the data, systemd user timers schedule the runs, and rclone reaches cloud drives. cairn adds defaults, a clean terminal UX and an agent skill. It never needs sudo, except for the package reinstall step of `rebuild`, which asks first.

## Concepts
| | snapshot | backup |
|---|---|---|
| purpose | get files and folders back | rebuild a machine after disk loss or a new filesystem |
| restic tag | `snapshot` | `backup` |
| default paths | `$HOME` | `$HOME`, `/etc`, `/usr/share/keyrings` |
| default every | `1h` | `daily` |
| default keep | `24h,7d,4w` | `7d,4w,12m` |
| after run | `forget` | write manifests before the run; `forget --prune` after |

- **Target**: one restic repo per user, shared by any number of hosts (snapshot queries and writes are scoped by host). There's no password by default (`--insecure-no-password`); `init --password` uses `~/.config/cairn/password`. Accepted forms:
  - a path (a mount from fstab, a NAS, a USB disk)
  - `remote:path`, an existing rclone remote (becomes `rclone:remote:path`)
  - a restic URL (`sftp:`, `s3:`, `b2:`, `rest:`, `azure:`, `gs:`, `swift:`, `rclone:`)
- **Manifests**: `~/.local/state/cairn/manifests/*.txt`, one name per line, rewritten before every backup for these managers when present: apt, snap, flatpak, brew, uv tools, pipx, cargo, npm -g. `crontab -l` is saved as well.
- **Excludes**: a built-in list of regenerable directories (venvs, caches, package stores, trash, container storage), plus `--exclude-caches --one-file-system`, user extras, the target (when it's local), and cairn's own rebuild scratch directory. Dotfiles and lockfiles are always kept.
- **pre-restore**: before any in-place restore, cairn snapshots the current version (tag `pre-restore`) and keeps those snapshots for 30 days.

## Commands
| command | does |
|---|---|
| `cairn` | TUI dashboard on a TTY; otherwise `status` |
| `cairn init <target> [--password] [--path P]...` | create or connect the repo, write the config, install the timers, enable linger, start the first snapshot |
| `cairn status [--json]` | target online, last/next/count/result for each job |
| `cairn snapshot` / `cairn backup` | run now (the timers run these) |
| `cairn restore <path> [--at A] [--to T] [-y]` | restore in place (preview + confirm) or to T |
| `cairn history <path> [--json]` | versions of a path |
| `cairn rebuild [--host H] [--from OLDHOME] [-y]` | fresh machine: restore the home, stage `/etc`, reinstall packages one confirmed step at a time |
| `cairn set <key> <value>` | `snapshot.every`, `snapshot.keep`, `snapshot.paths`, `backup.every`, `backup.keep`, `backup.paths`, `exclude` |
| `cairn --skill` | print the agent skill |
| `cairn targets [--json]` | destination examples and setup instructions; no connection or config changes; JSON is an array of `{name, summary, setup, detail}` |
| `cairn help [command]` / `<command> --help` | plain-language examples and flags without requiring setup |

- `--at`: `latest` (the default), a snapshot id, `2026-10-01`, `2026-10-01 14:00`, or an age such as `30m`, `6h`, `3d`, `2w`.
- `every`: `15m 30m 1h hourly 6h 12h daily weekly off`.
- `keep`: comma list of `<n>h <n>d <n>w <n>m <n>y`, plus a bare `<n>` for `--keep-last n`.
- Lists: `+x` adds, `-x` removes, and `a,b` replaces.
- Relative restore paths use the current directory. A deleted file can be restored without recreating it first. `-y` bypasses confirmation while preserving the safety snapshot for an existing path.

## Terminal experience

The CLI's interactive help and confirmation prompts and the TUI share a gold
stacked-stone mark. Redirected help stays plain text; JSON and the embedded
agent reference do not contain decorative output.

The dashboard has `s` snapshot, `b` backup, `r` restore, `e` settings, `t`
destination guides, `?` basic help, and `q` quit. Guides scroll with arrow keys;
lists paginate and filter with `/`. Destination selection opens examples and
instructions, leaving the configured target unchanged. Help explains snapshots,
full backups, restore safety, retention, and status badges in basic terms.

Supported destinations include local paths and mounted SMB/NFS, Google Drive
and other writable rclone remotes, direct rclone SMB, and native restic URLs.
Authentication is configured outside cairn. Mounted folders may also be source
paths when explicitly added; cloud source data must be mounted or synced first.

**Exit codes**:
- 0: ok
- 1: error
- 2: bad usage, or a confirmation is needed and there's no TTY (pass `-y`)
- 3: finished, but some files were unreadable
- 10: target offline

A timer run against an offline target exits 0 and says so.

## restic mapping
`R = restic -r REPO {--insecure-no-password | --password-file F} [-o rclone.program=BIN]`. `RESTIC_*` variables are removed from restic's environment.

- **init**: `R cat config` → 0 connect, 10 `R init`, 12 wrong password, anything else abort. For a local target the parent dir must exist.
- **run**: `R backup --json --host H --tag K --one-file-system --exclude-caches --retry-lock 10m --exclude … PATHS`
  - Missing paths are dropped beforehand.
  - Exit 3 with only system-path errors (`/etc`, keyrings) counts as success.
  - Exit 10 means offline.
- **forget**: `R forget --host H --tag K --group-by host --keep-… --retry-lock 30m`. About once a day (state file `pruned`) it adds `--prune` and expires `pre-restore` snapshots (`--keep-within 30d`).
- **restore**:
  - resolve: covering snapshots ≤ the `--at` time, newest first, checked with `R ls S P`
  - preview: `R restore S:DIR --target DIR --include /BASE --dry-run --json --verbose=2`
  - then `R backup --tag pre-restore P`, then the real restore
  - `--to` an existing dir: `restore S:DIR --target TO --include /BASE`; a new file name: `R dump --target TO S P`; a new dir name: `R restore S:P --target TO`
- **history**: file: `R find --json --host H P` (exact-path matches only); dir: the covering snapshots.
- **rebuild**:
  - `R restore S:OLDHOME --target $HOME`
  - stage `/etc` and keyrings under `~/.local/state/cairn/rebuild`
  - re-apply the config and timers
  - the confirmed steps: apt sources, `apt update`, `apt install` (filtered by `apt-cache pkgnames`), snap, flatpak, brew, uv, pipx, cargo, npm, crontab

## Scheduling
`~/.config/systemd/user/cairn-{snapshot,backup}.{service,timer}`:
- The service is `Type=oneshot`, `Nice=19`, `IOSchedulingClass=idle`, `CPUQuota` = 50% of cores, `SuccessExitStatus=3`.
- The timer uses `OnCalendar` and `Persistent=true`.
- `every` mapping: `15m *:0/15`, `30m *:0/30`, `1h hourly`, `6h 0/6:00`, `12h 0/12:00`, `daily`, `weekly`.
- Jobs run one at a time (a flock on `~/.local/state/cairn/lock`). Progress goes to `~/.local/state/cairn/<job>.progress`, which the TUI reads.

## Files
- `~/.config/cairn/config.json`: the config
- `~/.config/cairn/password`: optional
- `~/.local/share/cairn/bin`: downloaded restic 0.19.1 / rclone 1.75.2, checked against SHA256SUMS. A system restic ≥ 0.17 is used when present.

## Hardening (from the audit)
- Every restic call passes repo-supplied values after `--`. The repo goes in via `RESTIC_REPOSITORY`, not argv, and read-only calls use `--no-lock`.
- restic and rclone downloads are checked against SHA256s compiled into cairn, with a client timeout and a size cap.
- Manifest names from a backup must match `^[A-Za-z0-9@][A-Za-z0-9@._+:/=-]*$` (no `/` for apt/snap) before they reach any install command. `sudo sh -c` takes its paths as positional args.
- Paths and names from the repo are stripped of control characters before printing.
- `--at`: a unique id prefix (4+ hex chars) wins, then dates, then ages (bounded). If the newest covering snapshot lacks the path, a single `find` looks further back.
- `init` records `since`. `rebuild` only trusts snapshots from before that, takes the home from the newest snapshot or backup that has it, and keeps the old config's settings (paths re-homed).
- Timer units carry the user's PATH (so manifests can find cargo, brew, nvm…). ExecStart is quoted and checked for systemd specials.
- A local target on the same device as `$HOME` needs confirmation or `-y`.

## Supported builds

`scripts/install.sh` installs the latest Linux amd64 or arm64 release into
`~/.local/bin` without sudo, after verifying the archive against the release's
SHA-256 checksum. `CAIRN_VERSION=vX.Y.Z` selects a release and
`CAIRN_INSTALL_DIR` selects the destination. Unsupported platforms and checksum
failures exit without installing a binary. Installation does not initialize
a repository or change backup configuration.

Releases provide statically linked Linux amd64 and arm64 binaries. Automatic
dependency downloads cover those architectures. systemd user timers are needed
for automatic scheduling. If scheduling setup fails, init saves the config,
warns, and succeeds; manual snapshot, backup, and restore remain available.
CLI startup is checked separately across Linux
distributions. `rebuild` only offers installed package managers.
