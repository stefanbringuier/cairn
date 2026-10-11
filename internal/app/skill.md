---
name: cairn
description: Back up, snapshot and restore files, folders or a whole machine with the `cairn` CLI (restic underneath). Use for "restore this file", "get my folder back from yesterday", "back this up", "change backup frequency/retention", "set up backups to my NAS/USB/Google Drive", or "rebuild my setup on a new machine".
---

# cairn

cairn is a per-user Linux wrapper around restic. Backups need no sudo; package recovery may ask for it. Everything below is the full interface: you don't need any other docs.

**Two kinds of runs:**
- **snapshot** (tag `snapshot`): frequent; default paths `$HOME`; default every `1h`, keep `24h,7d,4w`. Use these to get files and folders back.
- **backup** (tag `backup`): a full rebuild recipe: `$HOME`, `/etc`, `/usr/share/keyrings`, plus package manifests (apt, snap, flatpak, brew, uv tools, pipx, cargo, npm -g, crontab). Default every `daily`, keep `7d,4w,12m`. Use these to rebuild a machine on a new disk or filesystem.

**Generated stuff is skipped:** `.venv`, `node_modules`, `__pycache__`, `.cache`, package-manager stores, trash, container storage, and any directory with a `CACHEDIR.TAG`. Dotfiles and lockfiles are kept, so the environment can be regenerated.

## Rules for agents
- Never run a command that needs confirmation without `-y` unless a human is at the terminal. Without a TTY and without `-y`, cairn refuses with exit 2 and changes nothing.
- Prefer `--json` for reading state.
- Restoring in place overwrites the current file or folder. cairn first saves the current version as a `pre-restore` snapshot (kept about 30 days) and prints `undo: cairn restore <path> --at <id>`. Files newer than the snapshot that aren't in it are never deleted.
- To recover something without touching the current copy, use `--to <somewhere>`.
- `cairn rebuild -y` runs `sudo apt install …` and similar without asking. Only use it when the user has explicitly agreed.

## Commands
| Command | Does |
|---|---|
| `cairn` | TUI dashboard if a TTY, else the same as `cairn status` |
| `cairn init <target> [--password] [--path P]... [-y]` | Create the repo or connect to an existing one; write the config; install the systemd user timers; start the first snapshot in the background. Re-running it is safe (keeps settings, re-points the target). If a local target is on the same disk as `$HOME` (e.g. an unmounted NAS mount point), it needs confirmation or `-y` |
| `cairn status [--json]` | Target online/offline; for snapshot and backup: every, keep, paths, count, last (time + id), next run, result of the last timer run (`ok`/`partial`/`failed`), running + percent, stale (overdue). Exits 10 when offline |
| `cairn snapshot` | Run a snapshot now (blocks; prints a summary) |
| `cairn backup` | Run a full backup now |
| `cairn restore <path> [--at A] [--to T] [-y]` | Restore a file or folder |
| `cairn history <path> [--json]` | Versions of a path: `[{snapshot, kind, time, size, mtime}]`, newest first (size/mtime only for files; `[]` if none) |
| `cairn rebuild [--host H] [--from OLDHOME] [-y]` | On a new machine after `cairn init <same target>`: restores the old home into `$HOME`, stages the old `/etc` in `~/.local/state/cairn/rebuild/etc`, then reinstalls packages step by step (asks before each) |
| `cairn set [<key> <value>] [--json]` | Change a setting, or show all settings with no args |
| `cairn --skill` | This document |
| `cairn --version` | Print the version |
| `cairn targets [--json]` | Explain destination setup without connecting or changing configuration; JSON is an array of `{name, summary, setup, detail}` |
| `cairn help [command]` / `<command> --help` | Plain-language examples and flags; works before init |

**Target forms:**
- A path (a mounted NAS from fstab, a USB disk: `/mnt/nas/cairn`). Its parent must exist.
- An rclone remote name plus path (`gdrive:backups`). The remote must already exist: a human runs `rclone config` once.
- Any restic URL: `sftp:user@host:/path`, `s3:...`, `b2:...`, `rest:...`.
- SMB/NFS mounts work as local paths. Direct SMB through rclone uses `nas:share/path` after `rclone config`; Google Drive uses `gdrive:backups` and requires a user-configured Google API client ID. Other writable remotes include Dropbox, OneDrive, and WebDAV. Native restic schemes also include `azure:`, `gs:`, and `swift:`.
- Destinations hold restic repositories. Source paths are local; mount or sync remote source data first and explicitly add mounted sources with `cairn set snapshot.paths +/mnt/data`.
- Scheduled cloud access needs credentials available to the systemd user manager. Init warns and succeeds if scheduling is unavailable; snapshot, backup, and restore can still run manually.

restic and rclone are downloaded automatically (pinned and hash-checked) into `~/.local/share/cairn/bin` if they're missing.

**Passwords:** repos have no password by default (anyone who can read the target can read the data).
- `--password` makes cairn prompt for one, or generate one (without a TTY it's generated, and the path to it is printed).
- To connect to an existing passworded repo non-interactively, write the password to `~/.config/cairn/password` (mode 600) first, then run `cairn init <target> --password`.

**`--at`:**
- `latest` (the default).
- A snapshot id or a unique prefix of at least 4 characters, from `history`/`status`; it wins over every other form.
- `2026-10-01` (as of the end of that day) or `"2026-10-01 14:00"`.
- An age: `30m`, `6h`, `3d`, `2w` (`m` = minutes here).

cairn picks the newest snapshot at or before that moment that contains the path, and looks further back if newer ones don't have it.

Relative paths such as `cairn restore notes.txt` resolve from the current directory. The file may have any extension or be deleted. `-y` skips the preview/confirmation step but keeps the in-place safety snapshot; it does not bypass restore protection. Flags work before or after the path.

**`--to`:** an existing directory (the item lands inside it) or a new path (written under that name). Overwriting an existing item asks first, or needs `-y`.

**set keys:**
- `snapshot.every`, `backup.every`: one of `15m 30m 1h 6h 12h daily weekly off` (aliases: `hourly`, `1d`, `1w`). Timers update immediately.
- `snapshot.keep`, `backup.keep`: a comma list of `<n>h <n>d <n>w <n>m <n>y` (hourly/daily/weekly/monthly/yearly; `m` = months here), plus an optional bare `<n>` = keep the last n. E.g. `8,48h,14d,8w,24m,5y`.
- `snapshot.paths`, `backup.paths`: `+PATH` adds, `-PATH` removes, `A,B` replaces.
- `exclude`: restic patterns; same `+`/`-`/replace syntax, e.g. `+*.iso`, `+/home/me/Videos`.

**Exit codes:**
- 0: ok
- 1: error
- 2: bad usage, or a confirmation was needed (rerun with `-y`)
- 3: done, but some files couldn't be read
- 10: target offline (no repository at the path, e.g. the NAS isn't mounted)

Scheduled (timer) runs against an offline target exit 0 and log "offline, skipped".

## Recipes
- **Restore a file as it was yesterday, keeping the current one:**
  `cairn restore ~/notes.md --at 1d --to /tmp/notes-yesterday.md`
- **Undo changes to a folder** (find the version first, then restore it):
  `cairn history ~/proj/docs --json`, then `cairn restore ~/proj/docs --at <snapshot> -y`
- **Undo a restore:** run the `undo:` line it printed, i.e. `cairn restore <path> --at <pre-restore id> -y`.
- **Back up an extra folder too:**
  `cairn set backup.paths +/data/projects`, plus `cairn set snapshot.paths +/data/projects` if it should be in the frequent snapshots as well.
- **Snapshot every 15 minutes:**
  `cairn set snapshot.every 15m && cairn set snapshot.keep 8,24h,30d,8w`
- **Stop scheduled runs:**
  `cairn set snapshot.every off && cairn set backup.every off`
- **Is it working?**
  `cairn status --json`. Check `online`, `snapshot.last`, `snapshot.stale`, `backup.result`.
- **New or wiped machine:** install the OS, mount the same target, then:
  1. `cairn init <same target>`
  2. `cairn rebuild` (a human approves the sudo steps)
  3. `cairn restore <extra path> --at <id>` for any non-home paths it lists.
- **Logs of scheduled runs:** `journalctl --user -u cairn-snapshot -u cairn-backup -n 50`.

## Without cairn (fallback)
The repo is plain restic, with no password unless `~/.config/cairn/password` exists. `<repo>` is the `repo` field of `cairn set --json`; rclone repos also need `-o rclone.program=<path to rclone>`.
```
RESTIC_REPOSITORY=<repo> restic --insecure-no-password snapshots
RESTIC_REPOSITORY=<repo> restic --insecure-no-password restore latest --host <host> --tag backup --target ~/rescue
```
