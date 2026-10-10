<div align="center">

# cairn

**Snapshots and full-machine backups in two commands.**

restic underneath · no sudo · no config files to write · built for humans *and* AI agents

[![ci](https://github.com/stefanbringuier/cairn/actions/workflows/ci.yml/badge.svg)](https://github.com/stefanbringuier/cairn/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/stefanbringuier/cairn?sort=semver)](https://github.com/stefanbringuier/cairn/releases/latest)
[![license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

</div>

```text
╭──────────────────────────────────────────────────────────────────────╮
│                                                                      │
│  cairn  /mnt/nas/cairn  ● online  host tars                          │
│                                                                      │
│    snapshot  every 1h · keep 24h,7d,4w                               │
│              last 12 minutes ago 3f2a1b0c ✓ · next 48 minutes from   │
│              now · 31 kept                                           │
│    backup    every daily · keep 7d,4w,12m                            │
│              last 9 hours ago 8e744d19 ✓ · next 15 hours from now ·  │
│              11 kept                                                 │
│                                                                      │
╰──────────────────────────────────────────────────────────────────────╯
s snapshot now · b backup now · r restore · e settings · q quit
```

A cairn is a stack of stones that marks a trail, so you can always find your way back. This tool does the same for your files.

cairn wraps [restic](https://restic.net) (deduplicated, verifiable backups), **systemd user timers** (scheduling) and [rclone](https://rclone.org) (Google Drive and other cloud targets). It doesn't reimplement any of them: it picks good defaults, keeps the commands short, and gives you a TUI.

## Install

```sh
curl -fsSL https://github.com/stefanbringuier/cairn/releases/latest/download/cairn_linux_amd64.tar.gz | tar -xz -C ~/.local/bin cairn
```

Use `cairn_linux_arm64.tar.gz` on ARM. You can also run `go install github.com/stefanbringuier/cairn@latest`.

You don't need to install restic or rclone first: if they're missing, cairn downloads a pinned, hash-checked copy into `~/.local/share/cairn/bin`. It needs Linux with systemd.

## Two commands

```sh
cairn init /mnt/nas/cairn     # any mounted path, a NAS, a USB disk, gdrive:backups, sftp:…, s3:…
cairn                         # the dashboard
```

That's it:
- **Snapshots** of your home run every hour.
- **Full backups** run every day.
- Old versions are thinned out automatically.
- The first snapshot starts right away in the background.

## Everyday use

| You want to… | Run |
|---|---|
| see what's going on | `cairn` (or `cairn status`) |
| get a file or folder back | `cairn restore ~/notes.md` |
| …as it was 3 days ago | `cairn restore ~/notes.md --at 3d` |
| …without touching the current copy | `cairn restore ~/proj --at 2026-10-01 --to /tmp/old` |
| see every version | `cairn history ~/notes.md` |
| back up right now | `cairn snapshot` or `cairn backup` |
| snapshot more often | `cairn set snapshot.every 15m` |
| keep more history | `cairn set backup.keep 14d,8w,24m,5y` |
| add a folder | `cairn set snapshot.paths +/data/projects` |
| skip something | `cairn set exclude +'*.iso'` |

**Restoring in place is safe:**
- cairn first shows what would change ("overwrite 3 changed · recreate 1 missing").
- It asks before going ahead; pass `-y` to skip the question.
- It snapshots your current version before overwriting, so every restore can be undone. The command to undo it is printed afterwards.
- Files newer than the snapshot that aren't in it are never deleted.

## Snapshots vs backups

| | **snapshot** | **backup** |
|---|---|---|
| for | getting files and folders back | rebuilding a machine after a disk dies |
| what | `$HOME` (plus any paths you add) | `$HOME`, `/etc`, `/usr/share/keyrings`, and lists of your installed packages (apt, snap, flatpak, brew, uv tools, pipx, cargo, npm -g, crontab) |
| default | every hour, keep `24h,7d,4w` | every day, keep `7d,4w,12m` |

Both kinds go to the **same repository**, so files they share are stored only once. Several machines can also share one repository: everything is tracked per host.

Regenerable stuff is skipped: `.venv`, `node_modules`, `__pycache__`, `.cache`, package-manager stores, trash, container storage, and anything marked with a `CACHEDIR.TAG`. Dotfiles and lockfiles (`pyproject.toml`, `uv.lock`, `package.json`, …) are always kept, so the environment can be regenerated.

## A new machine, the same setup

Install the OS, mount the same target, then:

```sh
cairn init /mnt/nas/cairn     # connects to the existing repository
cairn rebuild
```

`rebuild` does three things:
1. It restores your old home into the new one. This works whatever the new disk, partitioning or filesystem.
2. It puts the old `/etc` in `~/.local/state/cairn/rebuild/etc` for reference.
3. It walks you through reinstalling your packages, one confirmed step at a time: apt sources and keys, then `apt`, `snap`, `flatpak`, `brew`, `uv`, `pipx`, `cargo`, `npm` and your crontab.

These steps are the only place cairn uses sudo, and you approve each one. Anything else that was in the backup is listed with the command to restore it.

## Targets

| Target | How |
|---|---|
| a mounted drive or NAS (fstab, CIFS, NFS, USB) | `cairn init /mnt/nas/cairn` |
| Google Drive, Dropbox, OneDrive, … | `rclone config` once, then `cairn init gdrive:backups` |
| SFTP / S3 / B2 / Azure / GCS / REST server | `cairn init sftp:me@host:/srv/cairn` (any [restic repository URL](https://restic.readthedocs.io/en/stable/030_preparing_a_new_repo.html)) |

**Encryption is off by default.** Anyone who can read the target can read your files. That's fine for a drive you own. For cloud or shared targets, use `cairn init <target> --password`, and keep that password somewhere other than this machine.

If a local target is on the same disk as your home, cairn asks before using it. That catches an unmounted NAS mount point before it silently fills your system disk.

## Schedules and retention

- **Frequency** (`snapshot.every`, `backup.every`): `15m 30m 1h 6h 12h daily weekly off`.
  - Runs that were missed while the machine was off happen when it comes back.
  - Runs use low CPU and I/O priority.
  - Only one run happens at a time.
- **Retention** (`snapshot.keep`, `backup.keep`): a comma list of `<n>h <n>d <n>w <n>m <n>y`, meaning keep that many hourly, daily, weekly, monthly or yearly snapshots. A bare number keeps the last N, for example `8,24h,7d` for 15-minute snapshots. Old data is pruned about once a day.
- **When a run uses the target:** scheduled runs skip quietly when the target is offline, and the dashboard flags any job that's overdue.

## For AI agents

```sh
cairn --skill
```

That prints a self-contained skill file: every command and flag, the JSON shapes, exit codes and step-by-step recipes. Point your agent at it and it can drive cairn with no other docs.

- Every read command takes `--json`.
- Without a TTY, nothing destructive happens without `-y`: cairn exits 2 instead of prompting.

## How it works

```text
cairn init ─▶ ~/.config/cairn/config.json
           ─▶ ~/.config/systemd/user/cairn-{snapshot,backup}.{service,timer}
timer ─▶ cairn snapshot|backup ─▶ restic backup --json … ─▶ restic forget [--prune]
cairn restore ─▶ restic restore --dry-run (preview) ─▶ restic backup (safety) ─▶ restic restore
```

The repository is plain restic, so you can always use restic directly:

```sh
RESTIC_REPOSITORY=/mnt/nas/cairn restic --insecure-no-password snapshots
```

## Uninstall

```sh
cairn set snapshot.every off && cairn set backup.every off
rm -f ~/.config/systemd/user/cairn-* ~/.local/bin/cairn && rm -rf ~/.config/cairn ~/.local/state/cairn ~/.local/share/cairn
```

Your backups stay in the target until you delete them.

## Development

```sh
go test ./...                              # unit tests (CAIRN_NET_TEST=1 also checks the pinned download)
golangci-lint run && govulncheck ./...     # the same checks CI runs
go build -o ~/.local/bin/cairn .
```

The release process:
- **Versions come from commit messages** ([Conventional Commits](https://www.conventionalcommits.org)): `fix:` → patch, `feat:` → minor, `feat!:` or a `BREAKING CHANGE:` footer → major. `docs:`, `chore:`, `ci:` and the like don't release.
- **Every push to `main` with a releasable commit** gets tagged `vX.Y.Z`, built for linux/amd64 and arm64, and published with checksums and an auto-generated changelog.
- **CI checks every commit** for a conventional subject, the maintainer's author and committer identity, and no attribution trailers. So merge PRs locally (`git merge --ff-only` after rebasing, then push) rather than with the web button.

See [SECURITY.md](SECURITY.md) for how to report a vulnerability.

## License

[MIT](LICENSE) © Stefan Bringuier
