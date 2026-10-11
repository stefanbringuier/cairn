package app

import (
	"fmt"
	"io"
	"strings"
)

const usage = `cairn — a way back to your files, with restic as your trail

  cairn                  Open the dashboard (or print status outside a terminal).
  cairn init <target>    Choose where to save your backups and start a snapshot.
  cairn snapshot        Save a version of your home and added folders now.
  cairn backup          Also save system settings and installed-package lists.
  cairn restore <path>  Get a file or folder back; preview and confirm first.
  cairn history <path>  See its saved versions.
  cairn status          See the destination, schedules, and backup health.
  cairn set             See your settings; add a key and value to change one.
  cairn rebuild         Recover your home and walk through reinstalling packages.
  cairn targets         See local, NAS, and cloud destination setup examples.
  cairn --skill         Print the command reference for agents.
  cairn --version       Print the installed version.

Try cairn help restore or cairn restore --help for examples and flags.
In the dashboard: ? explains the basics; t shows storage options.
Linux. Automatic schedules use systemd user timers; sources are local paths.
`

type storageChoice struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Setup   string `json:"setup"`
	Detail  string `json:"detail"`
}

func (s storageChoice) Title() string       { return s.Name }
func (s storageChoice) Description() string { return s.Summary }
func (s storageChoice) FilterValue() string { return s.Name }

var storageChoices = []storageChoice{
	{"Local disk or USB drive", "A folder on a disk you can write to.", "cairn init /mnt/backup/cairn", "Mount the disk first. A copy on your home disk helps undo mistakes, but cannot survive that disk failing."},
	{"Mounted SMB / NFS NAS", "A network share mounted as a local folder.", "cairn init /mnt/nas/cairn --password", "Mount the share first and keep it available for scheduled runs. Mounted folders can also be sources: cairn set snapshot.paths +/mnt/data"},
	{"Google Drive", "A Google Drive remote configured with rclone.", "rclone config\ncairn init gdrive:backups --password", "Create an rclone remote named gdrive, choose Google Drive, and authorize it in your browser. Use your own Google API client ID: https://rclone.org/drive/#making-your-own-client-id"},
	{"SMB NAS through rclone", "Reach a share directly, without a local mount.", "rclone config\ncairn init nas:share/cairn --password", "Create an rclone remote named nas, choose SMB, and supply the server and credentials. The first path segment (share) is the share name."},
	{"SFTP over SSH", "Save backups on a server or NAS with SSH access.", "cairn init sftp:me@host:/srv/cairn --password", "Set up SSH access first. Scheduled runs need unattended authentication, usually an SSH key."},
	{"S3 and compatible storage", "Amazon S3, MinIO, Wasabi, and compatible services.", "cairn init s3:https://endpoint/bucket/cairn --password", "Create the bucket and configure provider credentials. Make credentials available to your systemd user manager so timers can use them."},
	{"More cloud destinations", "Dropbox, OneDrive, WebDAV, B2, Azure, GCS, Swift.", "rclone config\ncairn init remote:backups --password", "Use a writable rclone remote, or a native restic URL: b2:, azure:, gs:, swift:, rest:. Configure credentials before initializing."},
}

func targetGuide() string {
	var out strings.Builder
	out.WriteString("Where can I save backups?\n\n")
	for _, s := range storageChoices {
		fmt.Fprintf(&out, "%s — %s\n  %s\n  %s\n\n", s.Name, s.Summary, strings.ReplaceAll(s.Setup, "\n", "\n  "), s.Detail)
	}
	out.WriteString("These are backup destinations. Mount or sync remote files locally to use them as sources.\nUse --password for cloud or shared storage; keep the password somewhere safe.\n")
	return out.String()
}

func commandHelp(cmd string) string {
	switch cmd {
	case "restore":
		return `cairn restore <file-or-folder> [--at <version>] [--to <destination>] [-y]

Get the latest saved version back. A relative path is relative to your current
folder; the file can have any extension, and it can have been deleted.

  cairn restore notes.md                  Preview, then ask before restoring.
  cairn restore notes.md --at 3d          Get the version from three days ago.
  cairn restore notes.md --to /tmp/copy   Write a separate copy.
  cairn restore notes.md -y              Restore without asking.

  --at   latest (default), snapshot ID, date, or age such as 30m, 3d, 2w.
  --to   Write to a different file or folder instead of restoring in place.
  -y     Skip confirmation, including when run by a script or agent.

In-place restores save the current version first and print an undo command.
Newer files outside the snapshot are kept. Without a terminal, a confirmation
that cannot be shown exits 2; pass -y to proceed.
`
	case "init":
		return `cairn init <target> [--password] [--path <folder>]... [-y]

Create or connect a backup repository, install systemd user timers, and start
the first snapshot. Run cairn targets for NAS and cloud setup instructions.

  cairn init /mnt/backup/cairn
  cairn init gdrive:backups --password
  cairn init /mnt/backup/cairn --path ~/Projects --password

  --password  Protect backups with a password; save it off this machine.
  --path      Use this source instead of your home; repeat to add more.
  -y          Accept the same-disk warning without asking.

Mounted shares use a local path. Cloud targets need credentials configured
first. Password protection is off by default.
Without systemd user timers, run snapshots and backups manually.
`
	case "targets":
		return "cairn targets [--json]\n\n--json prints structured setup guides for scripts and agents.\n\n" + targetGuide()
	case "snapshot", "backup":
		return "cairn " + cmd + "\n\nSave a new version now. Snapshots cover your home and added folders. Full\nbackups also include system settings, keyrings, and package lists. Shared\nfiles are stored once; caches and disposable environments are skipped.\n"
	case "history":
		return "cairn history <file-or-folder> [--json]\n\nList saved versions, newest first. Use a snapshot ID with restore --at\nto pick one. --json prints structured output for scripts and agents.\n"
	case "status", "dashboard":
		return "cairn [status] [--json]\n\nSee where backups go, whether the target is reachable, and the last and\nnext runs. --json prints structured output. With no command, an interactive\nterminal opens the dashboard; ? explains it and t shows destination options.\n"
	case "set":
		return `cairn set [<key> <value>] [--json]

With no arguments, show your settings. Every controls how often a job runs;
keep controls how many hourly, daily, weekly, monthly, or yearly versions stay.

  cairn set snapshot.every 15m
  cairn set backup.keep 7d,4w,12m
  cairn set snapshot.paths +/mnt/data
  cairn set exclude +'*.iso'

Keys: snapshot.every, snapshot.keep, snapshot.paths, backup.every,
backup.keep, backup.paths, exclude. Frequencies: 15m, 30m, 1h, 6h, 12h,
daily, weekly, off. Lists: +value adds, -value removes, a,b replaces.
`
	case "rebuild":
		return "cairn rebuild [--host <old-host>] [--from <old-home>] [-y]\n\nRecover your old home on a new Linux machine, stage old system settings for\nreference, and walk through reinstalling packages. Each install step asks\nfirst and may need sudo. -y accepts those steps without asking.\n"
	default:
		return usage
	}
}

func printHelp(w io.Writer, cmd string) error {
	if isTTY() {
		if _, err := fmt.Fprintln(w, cliBrand()); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(w, commandHelp(cmd))
	return err
}
