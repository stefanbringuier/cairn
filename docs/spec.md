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

- **Target**: one restic repo per profile, shared by any number of hosts (snapshot queries and writes are scoped by host). There's no password by default (`--insecure-no-password`); `init --password` uses the selected profile's password file. Accepted forms:
  - a path (a mount from fstab, a NAS, a USB disk)
  - `remote:path`, an existing rclone remote (becomes `rclone:remote:path`)
  - a restic URL (`sftp:`, `s3:`, `b2:`, `rest:`, `azure:`, `gs:`, `swift:`, `rclone:`)
- **Manifests**: `~/.local/state/cairn/manifests/*.txt`, one name per line, rewritten before every backup for these managers when present: apt, snap, flatpak, brew, uv tools, pipx, cargo, npm -g. `crontab -l` is saved as well.
- **Excludes**: a built-in list of regenerable directories (venvs, caches, package stores, trash, container storage), plus `--exclude-caches --one-file-system`, user extras, every configured local repository, and every profile's rebuild scratch directory. Dotfiles and lockfiles are always kept.
- **pre-restore**: before replacing an existing destination, including `restore --to`, cairn snapshots its current contents (tag `pre-restore`) and keeps those snapshots for 30 days.

### Backup scope

`init --data-only` selects data-only scope. Both jobs initially use exactly the
repeated `--path` sources, or `$HOME` when none are given. No system folders are
added, no rebuild manifests are generated, and cairn's manifest directory is
excluded from both jobs even if old manifests are beneath a source. Normal
excludes, schedules, retention, host filtering, and restore safety still apply.
Explicitly chosen system folders remain valid data sources. Data-only backups
carry both `backup` and `data-only` tags.

The configuration persists `backup_scope` as `machine` or `data-only`; older
configurations without it mean `machine`. Settings and status JSON expose it;
rendered settings and the dashboard explain the scope. It is not a `set` key.
Removing system paths with `set backup.paths` does not change machine scope or
stop manifests; changing paths in data-only scope does not add system paths.

Reinitialization without `--data-only` preserves scope and other settings.
Supplying `--path` replaces both job source lists, adding `/etc` and
`/usr/share/keyrings` only in machine scope. Explicitly changing an existing
scope requires at least one `--path` to avoid silently adopting an old source
list. `--data-only=false --path P` returns to machine scope. Schedules,
retention, and excludes survive either change; historical snapshots remain in
the repository and can be inspected or recovered normally.

`rebuild` refuses a data-only configuration before repository access. Under
machine scope, it ignores snapshots tagged `data-only` when selecting a rebuild
recipe or home version. Use `history`, `recover`, or `restore` for data-only
backups. Untagged historical backups retain the legacy machine behavior.
Rebuild always stages package/crontab manifests from the chosen machine backup
in a fresh directory, including when that source lived under the old home.
Newer home snapshots and current manifests cannot supply or supplement its
recipe; a failed manifest export stops before package installation.

## Commands
| command | does |
|---|---|
| `cairn` | TUI dashboard on a TTY; otherwise `status` |
| `cairn init <target> [--password] [--data-only] [--path P]...` | create or connect the repo, write the config, install the timers, enable linger, start the first snapshot |
| `cairn status [--json]` | target online, last/next/count/result for each job |
| `cairn profiles [--json]` | list configured profiles, targets, frequencies, timer names and whether each timer is enabled |
| `cairn snapshot` / `cairn backup` | run now (the timers run these) |
| `cairn restore <path> [--at A] [--to T] [-y]` | restore in place (preview + confirm) or to T |
| `cairn history <path> [--json]` | versions of a path |
| `cairn diff <from> [<to>] [--path P] [--metadata] [--json]` | compare saved snapshots, with `to` defaulting to latest; read-only |
| `cairn recover <path> [--at A] [--to T] [--json]` | export a new copy; never replace an existing destination |
| `cairn rebuild [--host H] [--from OLDHOME] [-y]` | fresh machine: restore the home, stage `/etc`, reinstall packages one confirmed step at a time |
| `cairn set <key> <value>` | `snapshot.every`, `snapshot.keep`, `snapshot.paths`, `backup.every`, `backup.keep`, `backup.paths`, `exclude` |
| `cairn --skill` | print the agent skill |
| `cairn targets [--json]` | destination examples and setup instructions; no connection or config changes; JSON is an array of `{name, summary, setup, detail}` |
| `cairn cloud setup` | obtain rclone if needed and run its remote configuration wizard; no cairn repository or timer changes |
| `cairn help [command]` / `<command> --help` | plain-language examples and flags without requiring setup |

- `--at`: `latest` (the default), a snapshot id, `2026-10-01`, `2026-10-01 14:00`, or an age such as `30m`, `6h`, `3d`, `2w`.
- `every`: `15m 30m 1h hourly 6h 12h daily weekly off`.
- `keep`: comma list of `<n>h <n>d <n>w <n>m <n>y`, plus a bare `<n>` for `--keep-last n`.
- Lists: `+x` adds, `-x` removes, and `a,b` replaces.
- Relative restore paths use the current directory. A deleted file can be restored without recreating it first. `-y` bypasses confirmation while preserving the safety snapshot for an existing path.

## Profiles

Select a profile with `cairn --profile NAME <command>` (or `--profile=NAME`),
before the command. With no command, it opens that profile's dashboard. Selection
applies to init, status, settings, manual runs, history, diff, recover, restore,
and rebuild. There is no saved active choice: omission selects `default`.
On an interactive terminal only, an unqualified `cairn` or `cairn dashboard`
with no default config offers a startup chooser if named profiles exist. It
lists names, backup scopes and redacted destinations, sorted by name; `/`
filters names/destinations and Enter opens the highlighted profile. Accepting
filter text requires a separate Enter to open. Esc clears filtering or quits;
q quits outside filter entry and Ctrl+C always quits (exit 1). Even one profile
requires selection. The choice lasts for this invocation and writes no config
or timers. Invalid/unreadable default config and dangling config symlinks do
not fall back. Invalid named inventory aborts without silently omitting profiles.
The chosen config is revalidated before opening its dashboard.

Explicit `--profile` (including `--profile default`), non-TTY invocations,
`status`, `dashboard --json`, and all other commands never use the chooser.
Missing-config errors explain listing/selecting existing profiles as well as
initialization. The plural typo `--profiles` or `--profiles=NAME` exits 2 with
guidance to use `--profile NAME` or `cairn profiles`.
Names contain 1–48 lowercase ASCII letters, digits, or hyphens, begin with a
letter or digit, and end with a letter or digit. Invalid names exit 2 before
accessing configuration. Arguments after `--` remain literal.

The reserved `default` profile uses the existing config, password, state files,
and `cairn-{snapshot,backup}` units in place. Existing installations need no
file moves, credential changes, or timer replacement. Named profiles use
`$XDG_CONFIG_HOME/cairn/profiles/NAME/{config.json,password}` and
`$XDG_STATE_HOME/cairn/profiles/NAME/` for manifests, progress, pruning state,
and rebuild scratch. Shared dependency binaries stay under `$XDG_DATA_HOME/cairn/bin`.
Default XDG roots retain their usual home-directory locations.

Init only changes the selected profile. It preserves that profile's schedules,
retention and exclusions; `--path` replaces its source paths. Changing a
configured repository needs confirmation or `-y`, because subsequent runs and
retention use the new repository. Existing snapshots stay in the old repository;
its password is reused, never silently replaced. Use a new name to keep both
destinations scheduled or when they need different passwords. Invalid existing
configuration aborts init instead of replacing it with defaults. A failed init
keeps the old configuration and removes only a newly generated, unsaved password.

Profiles JSON is an array ordered by name, each entry containing
`{name, target, backup_scope, snapshot: {every, timer, enabled}, backup: {every, timer, enabled}}`.
`backup_scope` is `machine` or `data-only` for that profile. Data-only runs exclude
the default and every named profile's manifests, including stale state without
a current configuration.
`enabled` is a boolean, or null if systemd cannot report it. Missing configuration
is omitted; invalid configuration aborts the list. Status JSON adds `profile`.
Text status, settings, and the dashboard identify the selected profile. Printed
undo and follow-up commands retain named-profile selection. An undo path with
terminal controls is reported with its profile and snapshot ID rather than an
executable command using a changed path.

Init refuses a repository already configured by another profile (local paths
are compared after resolving symlink aliases; remote URLs are compared as
configured). Different profiles need distinct repositories because host/tag
retention could otherwise delete each other's snapshots. Remote aliases must
also refer to distinct storage; cairn cannot resolve provider-side aliases.
Unreadable or invalid saved profiles abort backup exclusion discovery and
restore protection checks rather than risk copying or replacing their repositories.

## Cloud setup

`cloud setup` accepts no extra arguments or flags. It uses the same rclone
selection as repository access: a binary on PATH, an existing private binary,
or a pinned, checksum-checked download. It runs only `rclone config`, inheriting
stdin, stdout, stderr, and rclone's normal configuration environment. The human
chooses the provider and owns authentication. Success exits 0; a setup failure
exits 1; unsupported arguments exit 2. It works before `init` and requires no
private binary directory on PATH. Downloads use
`${XDG_DATA_HOME:-$HOME/.local/share}/cairn/bin`.

Both `remote:path` and `rclone:remote:path` require a configured rclone remote.
Missing remotes and failed cloud connections give credential-safe guidance to
run `cairn cloud setup`. A failed connection does not change the current cairn
configuration or timers, preserves an existing repository password, and removes
a newly generated password. Rclone configuration remains owned by rclone.

## Agent inspection and recovery

`diff` accepts snapshot IDs, dates, and ages using the same syntax as `--at`.
Selections are scoped to the configured host; safety snapshots are selected
only by explicit ID. It compares saved versions, not the live working tree.
`--path` filters one absolute or relative file/folder using path boundaries.
`--metadata` includes file metadata changes. JSON is one object:
`{from, to, path?, changes: [{path, modifier}], summary: {added, removed,
modified, type_changed, metadata_changed, damaged}}`. `from` and `to` are full snapshot
IDs. Modifiers are restic's `+`, `-`, `M`, `T`, `U`, and `?`; counts are items
within the selected path, not repository-wide byte statistics. No changes is
a successful result with `changes: []`.
Warnings or errors reading a subtree abort the comparison even if restic exits
zero and prints statistics. `?`/`damaged` indicates possible damage, not a
confirmed diagnosis. History JSON also returns full snapshot IDs.

`recover` exports the selected file or folder to an exact new destination.
Without `--to`, it creates a private temporary recovery directory. It stages
the result, then publishes it using a Linux rename that refuses replacement;
existing files, folders, and symlinks cause failure even if they appear during
the export. Failed exports do not publish partial results. Unsupported
filesystems fail rather than fall back to an overwrite. `-y` is not accepted.
JSON is `{snapshot, source, destination}` with a full snapshot ID and absolute
paths. Reads use `--no-lock` so a repository may be supplied read-only; a
concurrent prune can cause recovery to fail without publishing the result.

Before any existing destination is replaced, including `restore --to`, its
current content must be saved successfully. Partial backups, invalid summaries,
or a missing undo snapshot abort replacement. Ordinary backup excludes do not
apply to these safety copies. A safety copy of a directory containing the
local backup repository is refused to avoid backing up the repository itself.
Destinations overlapping any configured local repository or cairn's configuration
root (including every profile and password) are refused to preserve undo access.
Protection also follows config/password file symlinks outside that root and
protects their targets and containing destinations. The undo command uses the
full snapshot ID. A missing restore destination is staged
and atomically published without replacement, preserving native node metadata
(full filesystem-root exports keep restic's subtree-root semantics)
and allowing deleted symbolic links to be recreated. Existing `--to` container
semantics are resolved once; the write uses the exact destination that was saved.
An undo command is printed before replacement starts, including on a later
CLI restore failure; the TUI includes undo information in a restore error.
Symbolic-link destinations are refused. These precautions are not a transaction with other processes
editing the same files.

The embedded skill requires agents to inspect and recover new copies first.
Replacing existing work requires explicit user authorization for the exact
source version and destination; `-y` is not evidence of that authorization.
A skill, command flag, or user-writable config is not an access-control
boundary. Preventing an unrestricted agent from bypassing cairn requires
permissions or an executor controlled outside the agent.

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
  - resolve `--to` containers once into an exact destination; save existing destination contents without ordinary excludes, require a complete summary and verified root node, and print the full undo ID before writing
  - same-name existing destinations use `R restore S:DIR --target DESTDIR --include /BASE`; renamed existing files use `R dump --target DEST S P`; renamed existing dirs use `R restore S:P --target DEST`
  - missing destinations use native restore into staging, then atomic no-replace publication (including deleted links and saved file/folder metadata)
- **recover**: files use `R dump --no-lock --target STAGING S P` with mode 0600; folders use `R restore --no-lock S:P --target STAGING`. Publish only with no-replace rename. File copies contain bytes for inspection, rather than executable permissions or original timestamps; folder export root metadata follows restic's subtree export semantics.
- **diff**: `R diff --json --no-lock [--metadata] FROM TO`; reject reported read errors and incomplete output, filter by path boundaries, and return one structured object.
- **history**: file: `R find --json --host H P` (exact-path matches only); dir: the covering snapshots.
- **rebuild**:
  - the selected profile supplies the repository and recipe; the home restore
    can also restore saved config/unit files for other profiles, so inspect
    every profile and schedule afterward; selection does not limit its write scope
  - `R restore S:OLDHOME --target $HOME`
  - stage `/etc` and keyrings under `~/.local/state/cairn/rebuild`
  - re-apply the config and timers
  - the confirmed steps: apt sources, `apt update`, `apt install` (filtered by `apt-cache pkgnames`), snap, flatpak, brew, uv, pipx, cargo, npm, crontab

## Scheduling
`$XDG_CONFIG_HOME/systemd/user/cairn-{snapshot,backup}.{service,timer}` for default,
or `cairn-NAME-{snapshot,backup}.{service,timer}` for named profiles:
- The service is `Type=oneshot`, `Nice=19`, `IOSchedulingClass=idle`, `CPUQuota` = 50% of cores, `SuccessExitStatus=3`.
- The timer uses `OnCalendar` and `Persistent=true`.
- `every` mapping: `15m *:0/15`, `30m *:0/30`, `1h hourly`, `6h 0/6:00`, `12h 0/12:00`, `daily`, `weekly`.
- Jobs across all profiles sharing a state root run one at a time (the legacy
  flock on `$XDG_STATE_HOME/cairn/lock`), including retention/pruning. Restic's
  repository locks still serialize access from other clients. Progress and
  `pruned` markers belong to the selected profile. Inspection and recovery
  retain read-only behavior.
- ExecStart explicitly selects the profile. Services carry resolved absolute
  XDG config, state, data and cache roots and the user's PATH. Unsafe systemd
  characters in executable/root paths abort schedule installation before writes.
- After enabling or disabling, the timer's `ActiveState` must be `active`, or
  `inactive` for `off`. Where systemctl believes it runs in a chroot (some
  sandboxes and containers) it exits 0 while ignoring daemon-reload and start or
  stop, writing only enable symlinks; that counts as a scheduling failure.

## Files
- `~/.config/cairn/config.json`: the config
- `~/.config/cairn/password`: optional
- `~/.local/share/cairn/bin`: downloaded restic 0.19.1 / rclone 1.75.2, checked against SHA256SUMS. A system restic ≥ 0.17 is used when present.

## Hardening (from the audit)
- Every restic call passes repo-supplied values after `--`. The repo goes in via `RESTIC_REPOSITORY`, not argv, and read-only calls use `--no-lock`.
- restic and rclone downloads are checked against SHA256s compiled into cairn, with a client timeout and a size cap.
- Manifest names from a backup must match `^[A-Za-z0-9@][A-Za-z0-9@._+:/=-]*$` (no `/` for apt/snap) before they reach any install command. `sudo sh -c` takes its paths as positional args.
- Paths and names from the repo are stripped of control characters before printing.
- Arguments after the CLI `--` delimiter remain literal paths, including flag-like names.
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
a repository or change backup configuration. Rerunning the installer updates
the binary to the latest release, or the selected `CAIRN_VERSION`; there is no
automatic background updater.

Releases provide statically linked Linux amd64 and arm64 binaries. Automatic
dependency downloads cover those architectures. systemd user timers are needed
for automatic scheduling. If scheduling setup fails, including when the user
manager does not actually start or stop a timer, init saves the config, warns,
skips the background first snapshot, and succeeds; `set` reports the error.
Manual snapshot, backup, and restore remain available.
CLI startup is checked separately across Linux
distributions. `rebuild` only offers installed package managers.
