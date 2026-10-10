package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"
	"time"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

//go:embed skill.md
var skillMD string

var version = "dev" // set by the release build

const usage = `cairn: simple snapshots and full-machine backups (restic underneath)

  cairn                          dashboard (TUI)
  cairn init <target>            set up: a mounted path, an rclone remote (gdrive:x) or a restic URL
  cairn status [--json]
  cairn snapshot | backup        run now
  cairn restore <path> [--at latest|<id>|2026-10-01|3d] [--to <dir|file>] [-y]
  cairn history <path> [--json]
  cairn rebuild [-y]             new machine: restore home + reinstall packages
  cairn set [<key> <value>]      e.g. snapshot.every 30m, backup.keep 7d,4w,12m, snapshot.paths +~/Data
  cairn --skill                  everything an AI agent needs to drive cairn
  cairn --version
`

var (
	accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#E0AF68")).Bold(true)
	dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#7F8C98"))
	good   = lipgloss.NewStyle().Foreground(lipgloss.Color("#9ECE6A"))
	bad    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F7768E"))
	amber  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF9E64"))
	code   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7AA2F7"))
)

func ok(msg string)   { lipgloss.Println(good.Render("✓ ") + msg) }
func note(msg string) { lipgloss.Fprintln(os.Stderr, dim.Render("· "+msg)) }
func warn(msg string) { lipgloss.Fprintln(os.Stderr, amber.Render("! ")+msg) }

func bar(frac float64, width int) string {
	n := min(max(int(frac*float64(width)), 0), width)
	return good.Render(strings.Repeat("━", n)) + dim.Render(strings.Repeat("━", width-n))
}

func isTTY() bool { return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) }

type cliError struct {
	code int
	msg  string
}

func (e *cliError) Error() string { return e.msg }

var errCancelled = &cliError{1, "cancelled"}

func needsYes(what string) error {
	return &cliError{exitNeedsYes, what + ": no terminal to confirm in, rerun with -y"}
}

func confirm(title, desc, yes string) error {
	if !isTTY() {
		return needsYes(title)
	}
	var v bool
	if err := huh.NewConfirm().Title(title).Description(desc).Affirmative(yes).Negative("Cancel").Value(&v).Run(); err != nil || !v {
		return errCancelled
	}
	return nil
}

// parse lets flags appear before or after positional args.
func parse(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		_ = fs.Parse(args) // ExitOnError
		if args = fs.Args(); len(args) == 0 {
			return pos
		}
		pos, args = append(pos, args[0]), args[1:]
	}
}

func main() {
	args := os.Args[1:]
	cmd := "dashboard"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("cairn "+cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	yes := fs.Bool("y", false, "don't ask for confirmation")
	asJSON := fs.Bool("json", false, "machine-readable output")
	var err error
	switch cmd {
	case "--skill", "skill":
		fmt.Print(skillMD)
	case "--version", "version":
		if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && strings.HasPrefix(bi.Main.Version, "v") {
			version = bi.Main.Version[1:] // go install …@vX.Y.Z
		}
		fmt.Println("cairn " + version)
	case "-h", "--help", "help":
		fmt.Print(usage)
	case "init":
		pw := fs.Bool("password", false, "encrypt the repo with a password")
		var paths []string
		fs.Func("path", "back up this path instead of $HOME (repeatable)", func(v string) error { paths = append(paths, expand(v)); return nil })
		pos := parse(fs, args)
		if len(pos) != 1 {
			err = &cliError{exitNeedsYes, "usage: cairn init <target> [--password] [--path P]... [-y]"}
			break
		}
		err = cmdInit(pos[0], *pw, paths, *yes)
	case "dashboard", "status":
		parse(fs, args)
		var c Config
		if c, err = loadConfig(); err != nil {
			break
		}
		if cmd == "dashboard" && isTTY() {
			err = runTUI(c)
			break
		}
		st := getStatus(c)
		if *asJSON {
			b, _ := json.MarshalIndent(st, "", "  ")
			fmt.Println(string(b))
		} else {
			lipgloss.Println(renderStatus(st))
		}
		if !st.Online {
			err = &cliError{exitNoRepo, ""}
		}
	case "snapshot", "backup":
		parse(fs, args)
		var c Config
		if c, err = loadConfig(); err == nil {
			err = runJob(c, cmd, isTTY())
		}
	case "restore":
		at := fs.String("at", "latest", "which version: latest, a snapshot id, 2026-10-01[ 14:00], or an age like 3d")
		to := fs.String("to", "", "restore to this dir or file instead of in place")
		pos := parse(fs, args)
		var c Config
		if len(pos) != 1 {
			err = &cliError{exitNeedsYes, "usage: cairn restore <path> [--at A] [--to T] [-y]"}
		} else if c, err = loadConfig(); err == nil {
			err = cmdRestore(c, pos[0], *at, *to, *yes)
		}
	case "history":
		pos := parse(fs, args)
		var c Config
		if len(pos) != 1 {
			err = &cliError{exitNeedsYes, "usage: cairn history <path> [--json]"}
		} else if c, err = loadConfig(); err == nil {
			err = cmdHistory(c, expand(pos[0]), *asJSON)
		}
	case "rebuild":
		host := fs.String("host", "", "rebuild from this machine's backups")
		from := fs.String("from", "", "the old home path inside the backup")
		parse(fs, args)
		var c Config
		if c, err = loadConfig(); err == nil {
			err = cmdRebuild(c, *host, *from, *yes)
		}
	case "set": // no flag parsing: "-PATH" removes from a list
		pos := slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--json" || a == "-json" })
		var c Config
		if c, err = loadConfig(); err == nil {
			err = cmdSet(c, pos, len(pos) != len(args))
		}
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(exitNeedsYes)
	}
	exit(err)
}

func exit(err error) {
	if err == nil {
		return
	}
	code := 1
	var ce *cliError
	var re *resticError
	switch {
	case errors.As(err, &ce):
		code = ce.code
	case errors.As(err, &re) && (re.code == exitPartial || re.code == exitNoRepo):
		code = re.code
	}
	if msg := err.Error(); msg != "" {
		lipgloss.Fprintln(os.Stderr, bad.Render("✗ ")+msg)
	}
	os.Exit(code)
}

func cmdInit(target string, password bool, paths []string, yes bool) error {
	repo, err := repoFor(target)
	if err != nil {
		return err
	}
	if strings.HasPrefix(repo, "/") {
		parent := filepath.Dir(repo)
		if _, err := os.Stat(parent); err != nil {
			return fmt.Errorf("%s doesn't exist: is the drive mounted? (create it with mkdir -p if it should exist)", parent)
		}
		var a, b syscall.Stat_t
		if syscall.Stat(parent, &a) == nil && syscall.Stat(home, &b) == nil && a.Dev == b.Dev && !yes {
			// Also what an unmounted NAS/USB mount point looks like: don't silently fill the system disk.
			if err := confirm("The target is on the same disk as your home. Use it anyway?",
				"Fine for undoing mistakes, but it won't survive a disk failure. If this should be a NAS or USB drive, it isn't mounted.", "Use it"); err != nil {
				return err
			}
		}
	} else if !password && !strings.HasPrefix(repo, "rest:") {
		warn("no password: anyone who can read " + redact(target) + " can read your files (use --password for cloud targets)")
	}
	c, err := loadConfig()
	if err != nil {
		c = defaultConfig()
	}
	c.Target, c.Repo, c.Since = target, repo, time.Now()
	if len(paths) > 0 {
		c.Snapshot.Paths = paths
		c.Backup.Paths = append(slices.Clone(paths), "/etc", "/usr/share/keyrings")
	}
	created, shown := false, ""
	if password {
		if created, shown, err = setupPassword(); err != nil {
			return err
		}
	}
	r, err := newRestic(c)
	if err != nil {
		return err
	}
	_, err = r.out("cat", "config", "--no-lock")
	switch codeOf(err) {
	case 0:
		ok("connected to the existing repository at " + redact(target))
	case exitNoRepo:
		if _, err = r.out("init"); err == nil {
			ok("created a repository at " + redact(target))
		}
	case exitBadPass:
		if !password {
			err = fmt.Errorf("this repository has a password: put it in %s and rerun with --password", passwordFile())
		}
	default:
		err = fmt.Errorf("can't reach %s: %w", redact(target), err)
	}
	if err != nil {
		if created {
			os.Remove(passwordFile()) // a wrong or unused password must not stick around
		}
		return err
	}
	if shown != "" {
		if isTTY() {
			lipgloss.Println("password: " + accent.Render(shown))
			if !yes {
				_ = confirm("Saved that password somewhere safe?", "Without it the backups can't be opened, e.g. after this disk dies.", "Yes, saved")
			}
		} else {
			warn("generated a password in " + passwordFile() + ": copy it somewhere safe, the backups can't be opened without it")
		}
	}
	if err := c.save(); err != nil {
		return err
	}
	if err := applySchedule(c); err != nil {
		warn(err.Error() + " (scheduling off; run cairn snapshot/backup by hand)")
	} else {
		if hint := enableLinger(); hint != "" {
			warn(hint)
		}
		if err := startJob("snapshot"); err == nil {
			note("first snapshot running in the background: watch it with `cairn`")
		}
	}
	lipgloss.Println(renderSettings(c))
	return nil
}

// setupPassword makes sure the password file exists: an existing one is used as is (that's how you
// connect to a passworded repo), otherwise it's typed (TTY) or generated. shown is a generated password.
func setupPassword() (created bool, shown string, err error) {
	if _, err := os.Stat(passwordFile()); err == nil {
		return false, "", nil
	}
	var pw string
	if isTTY() {
		err := huh.NewInput().Title("Repository password").Description("Leave empty to generate a strong one.").
			EchoMode(huh.EchoModePassword).Value(&pw).Run()
		if err != nil {
			return false, "", errCancelled
		}
	}
	if pw == "" {
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		pw = base64.RawURLEncoding.EncodeToString(b)
		shown = pw
	}
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return false, "", err
	}
	return true, shown, os.WriteFile(passwordFile(), []byte(pw+"\n"), 0o600)
}

func cmdSet(c Config, pos []string, asJSON bool) error {
	if len(pos) == 2 {
		if err := c.set(pos[0], pos[1]); err != nil {
			return &cliError{exitNeedsYes, err.Error()}
		}
		if err := c.save(); err != nil {
			return err
		}
		if strings.HasSuffix(pos[0], ".every") {
			if err := applySchedule(c); err != nil {
				return err
			}
		}
	} else if len(pos) != 0 {
		return &cliError{exitNeedsYes, "usage: cairn set <key> <value>  (keys: " + strings.Join(setKeys, ", ") + ")"}
	}
	if asJSON {
		b, _ := json.MarshalIndent(c, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	lipgloss.Println(renderSettings(c))
	return nil
}

func cmdHistory(c Config, p string, asJSON bool) error {
	r, err := newRestic(c)
	if err != nil {
		return err
	}
	vs, err := history(r, c, p)
	if err != nil {
		return err
	}
	if asJSON {
		b, _ := json.MarshalIndent(vs, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(vs) == 0 {
		return fmt.Errorf("no versions of %s in any snapshot", tildify(p))
	}
	lipgloss.Println(accent.Render(tildify(p)) + dim.Render(fmt.Sprintf("  %d versions, newest first", len(vs))))
	for _, v := range vs {
		line := fmt.Sprintf("  %s  %-11s %-8s %s", v.Time.Local().Format("2006-01-02 15:04"), v.Kind, v.Snapshot, dim.Render(ago(v.Time)))
		if !v.Mtime.IsZero() {
			line += dim.Render(fmt.Sprintf("  · %d bytes, modified %s", v.Size, v.Mtime.Local().Format("2006-01-02 15:04")))
		}
		lipgloss.Println(line)
	}
	lipgloss.Println(dim.Render("  restore one: cairn restore " + tildify(p) + " --at <snapshot>"))
	return nil
}

func renderSettings(c Config) string {
	row := func(k, v string) string { return "  " + dim.Render(fmt.Sprintf("%-15s", k)) + v }
	lines := []string{accent.Render("cairn") + "  " + redact(c.Target) + dim.Render("  host "+c.Host)}
	for _, kind := range []string{"snapshot", "backup"} {
		j := c.job(kind)
		lines = append(lines, row(kind+".every", j.Every), row(kind+".keep", j.Keep), row(kind+".paths", tildifyAll(j.Paths)))
	}
	if len(c.Exclude) > 0 {
		lines = append(lines, row("exclude", strings.Join(c.Exclude, ", ")))
	}
	return strings.Join(lines, "\n")
}

func tildifyAll(ps []string) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = tildify(p)
	}
	return strings.Join(out, ", ")
}

func renderStatus(st Status) string {
	online := good.Render("● online")
	if !st.Online {
		online = bad.Render("○ offline") + dim.Render(" "+st.Error)
	}
	lines := []string{accent.Render("cairn") + "  " + st.Target + "  " + online + dim.Render("  host "+st.Host), ""}
	for _, kind := range []string{"snapshot", "backup"} {
		j := *st.job(kind)
		head := fmt.Sprintf("  %-9s %s", kind, dim.Render("every "+j.Every+" · keep "+j.Keep))
		var parts []string
		switch {
		case j.Running:
			parts = append(parts, code.Render(fmt.Sprintf("running %.0f%%", j.Percent*100)))
		case !st.Online:
			parts = append(parts, dim.Render("history unavailable while offline"))
		case j.Last != nil:
			last := "last " + ago(*j.Last) + " " + dim.Render(j.LastID)
			switch {
			case j.Stale:
				last = amber.Render("! ") + last + amber.Render(" (overdue)")
			case j.Result == "failed":
				last += bad.Render(" ✗ last run failed")
			case j.Result == "partial":
				last += amber.Render(" ! some files unreadable")
			default:
				last += good.Render(" ✓")
			}
			parts = append(parts, last)
		default:
			parts = append(parts, dim.Render("no "+kind+"s yet"))
		}
		if j.Next != nil {
			parts = append(parts, "next "+ago(*j.Next))
		} else if j.Every == "off" {
			parts = append(parts, dim.Render("schedule off"))
		}
		parts = append(parts, fmt.Sprintf("%d kept", j.Count))
		lines = append(lines, head, "            "+strings.Join(parts, dim.Render(" · ")))
	}
	if !st.Scheduler {
		lines = append(lines, "", amber.Render("! ")+dim.Render("systemd user timers unavailable: run cairn snapshot/backup yourself"))
	}
	return strings.Join(lines, "\n")
}
