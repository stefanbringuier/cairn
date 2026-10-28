# Agents and safe recovery

cairn helps an agent find earlier files without replacing today's work. Human
restores keep the familiar preview, one confirmation, and `-y` for approved
automation. Agents should start with a separate recovery copy.

## Inspect, recover, then review

```sh
cairn --skill
cairn history dog.mp4 --json
cairn diff <older-snapshot> <newer-snapshot> --path dog.mp4 --json
cairn recover dog.mp4 --at <candidate-snapshot> --json
```

Use the `destination` returned by recover to inspect the candidate. With
[ffprobe](https://ffmpeg.org/ffprobe.html) installed separately:

```sh
ffprobe -v error -show_format -show_streams -of json <recovered-destination>
```

A restic diff identifies content and metadata changes; it does not know a video's
duration or scene content. Recover candidate versions and measure them. For
“find dog.mp4 around 50 seconds,” return the matching copy and snapshot first.
Replacing the live video requires approval for that exact version and path.

## What the commands protect

| Operation | Protection |
|---|---|
| `history`, `diff` | Read saved snapshots without repository locks. Diff refuses reported read errors or incomplete output |
| `recover` | Stage a file or folder, then publish only to an absent destination using atomic no-replace rename. Refuse existing files, directories, or symlinks, including ones created during export. Failed exports leave no published partial copy |
| `restore`, including `--to` | Save existing destination contents in a complete, verified safety snapshot before replacement. Include normally excluded files. Refuse incomplete safety backups, symbolic-link destinations, and overlap with any configured local repository or the entire cairn configuration/password root |
| restore into a missing path | Publish a staged copy without replacing a destination that appears during recovery |

`recover --to` means the exact new name, unlike `restore --to`, which accepts
an existing directory as a container. Recover never accepts `-y`. Unsupported
atomic rename operations fail closed; choose a supported local recovery area.
Keep the printed undo command when restoring; it is available before CLI
replacement begins and in TUI failure messages after a safety copy succeeds.

With named profiles, explicitly prefix inspection and recovery commands with
`cairn --profile NAME`; omission always means the legacy `default` profile.
Check `cairn profiles --json` and the selected profile's `status --json` first.
Undo commands retain named-profile selection. Profiles have separate repository
passwords and state, but profile names are selectors, not permission boundaries.
Rebuild selects one repository and recipe while restoring the entire home,
including saved configuration and unit files for other profiles. Its broad
write scope requires authorization for the whole rebuild; inspect all profiles
and their schedules afterward.

A safety snapshot does not freeze your filesystem. Changes made between the
safety copy and replacement can still be lost. Pause editors and other writers
for an approved replacement, recheck changes since review, and prefer recovery
copies when that is not possible. Safety snapshots also need space and a
healthy repository; they are retained for about 30 days, not forever.

## Enforce access outside the agent

A skill supplies instructions. Flags, prompts, and a policy file editable by
the agent cannot constrain an unrestricted shell running as your Linux user.
That agent can invoke restic directly, change configuration, or write files
without cairn. A terminal prompt answered by the agent is not independent approval.

For enforced restrictions without an application sandbox, run the agent under
a separate Linux identity and grant only the permissions it needs:

- Read access to the intended project's repository, with read-only filesystem
  permissions or storage-provider credentials. A restic password decrypts the
  repository; it does not grant a read-only role. Host/path filters do not
  restrict access to other backups in the same repository.
- Read-only trusted cairn configuration and binaries. Keep sudo, owner
  credentials, full cloud/rclone credentials, configuration changes, pruning,
  and package rebuilds outside the agent.
- No write access to live files **or their parent directories**. Directory
  permissions can otherwise allow unlinking and replacing a read-only file.
- Write access to a separate recovery directory, plus private temporary and
  cache directories. Keep recovery ancestors trusted and stable; no-replace
  publication is not protection against an attacker changing parent paths.
- A human or independent approval broker that performs approved replacements
  with the owner's permissions after reviewing the selected version and
  current destination. The agent must not control that approval channel.

A trusted executor can expose only `history`, `diff`, and `recover`, accepting
structured arguments and enforcing allowed repositories and recovery paths.
An allowlist has value only when the agent cannot bypass it with an unrestricted
shell or untrusted binary/configuration. Read-only repository operations use
`--no-lock`; a concurrent prune may fail recovery rather than publish bad output.

These are deployment choices, not permissions installed by cairn. See
[Security](../SECURITY.md) for the product's other protections and
[usage](usage.md#for-ai-agents) for command examples.

## Mapped new-machine rebuild

For an explicitly approved rebuild, `--map OLD=NEW` uses exact absolute
paths and the same complete safety snapshots/protected destinations as restore.
A data-only profile skips home/package steps. Review every source version,
destination and overwrite before confirming; use only disposable fixtures when
testing. `-y` grants no new authority. Mapping failure leaves configuration/host
unchanged but does not roll back earlier successful restores. Whole-home
restoration remains a separate broad overwrite with partial-failure risks.

A printed undo receipt may name a safety snapshot made under the previous
configured host. `restore --at` accepts that exact full 64-hex ID only for a
single-path, solely `pre-restore` snapshot in the selected repository. The exact
saved path must match. This does not change credentials, host configuration,
ordinary snapshot selection or confirmation requirements; it is not general
cross-host browsing or permission isolation.
