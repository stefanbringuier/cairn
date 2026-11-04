package app

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
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
	lipgloss.Println(cliBrand())
	if err := huh.NewConfirm().Title(title).Description(desc).Affirmative(yes).Negative("Cancel").Value(&v).
		WithTheme(huh.ThemeFunc(settingsTheme)).Run(); err != nil || !v {
		return errCancelled
	}
	return nil
}

// parse lets flags appear before or after positional args.
func parse(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for len(args) > 0 {
		if args[0] == "--" {
			return append(pos, args[1:]...)
		}
		if !strings.HasPrefix(args[0], "-") || args[0] == "-" {
			pos, args = append(pos, args[0]), args[1:]
			continue
		}
		n := 1
		name, _, hasValue := strings.Cut(strings.TrimLeft(args[0], "-"), "=")
		if f := fs.Lookup(name); f != nil && !hasValue {
			boolean, ok := f.Value.(interface{ IsBoolFlag() bool })
			if !ok || !boolean.IsBoolFlag() {
				n = min(2, len(args))
			}
		}
		_ = fs.Parse(args[:n]) // ExitOnError
		args = args[n:]
	}
	return pos
}

// Run dispatches a command and returns its process exit status.
func Run(args []string, version string) int {
	explicitProfile := len(args) > 0 && (args[0] == "--profile" || strings.HasPrefix(args[0], "--profile="))
	name, args, err := selectProfile(args)
	if err != nil {
		return reportError(err)
	}
	previous := selectedProfile
	selectedProfile = name
	defer func() { selectedProfile = previous }()
	cmd := "dashboard"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		topic := ""
		if len(args) > 0 {
			topic = args[0]
		}
		return reportError(printHelp(os.Stdout, topic))
	}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" {
			return reportError(printHelp(os.Stdout, cmd))
		}
	}
	fs := flag.NewFlagSet(profileCommand()+" "+cmd, flag.ExitOnError)
	fs.Usage = func() { _ = printHelp(os.Stderr, cmd) }
	yes := fs.Bool("y", false, "don't ask for confirmation")
	asJSON := fs.Bool("json", false, "machine-readable output")
	switch cmd {
	case "--skill", "skill":
		fmt.Print(skillMD)
	case "--version", "version":
		if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && strings.HasPrefix(bi.Main.Version, "v") {
			version = bi.Main.Version[1:] // go install …@vX.Y.Z
		}
		fmt.Println("cairn " + version)
	case "targets":
		if len(parse(fs, args)) != 0 {
			err = &cliError{exitNeedsYes, "usage: cairn targets [--json]"}
		} else if *asJSON {
			b, _ := json.MarshalIndent(storageChoices, "", "  ")
			fmt.Println(string(b))
		} else {
			err = printHelp(os.Stdout, "targets")
		}
	case "profiles":
		if len(parse(fs, args)) != 0 {
			err = &cliError{exitNeedsYes, "usage: cairn profiles [--json]"}
		} else {
			err = cmdProfiles(*asJSON)
		}
	case "cloud":
		if len(args) != 1 || args[0] != "setup" {
			err = &cliError{exitNeedsYes, "usage: " + profileCommand() + " cloud setup"}
		} else {
			err = cmdCloudSetup()
		}
	case "init":
		pw := fs.Bool("password", false, "encrypt the repo with a password")
		dataOnly := fs.Bool("data-only", false, "save only configured sources, without machine rebuild additions")
		var paths []string
		fs.Func("path", "back up this path instead of $HOME (repeatable)", func(v string) error { paths = append(paths, expand(v)); return nil })
		pos := parse(fs, args)
		if len(pos) != 1 {
			err = &cliError{exitNeedsYes, "usage: " + profileCommand() + " init <target> [--password] [--data-only] [--path P]... [-y]"}
			break
		}
		var requestedScope *bool
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "data-only" {
				requestedScope = dataOnly
			}
		})
		err = cmdInit(pos[0], *pw, paths, *yes, requestedScope)
	case "dashboard", "status":
		parse(fs, args)
		var c Config
		interactive := cmd == "dashboard" && isTTY() && !*asJSON
		if interactive && !explicitProfile {
			c, err = dashboardConfig(chooseStartupProfile)
		} else {
			c, err = loadConfig()
		}
		if err != nil {
			break
		}
		if interactive {
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
			err = &cliError{exitNeedsYes, "usage: " + profileCommand() + " restore <path> [--at A] [--to T] [-y]"}
		} else if c, err = loadConfig(); err == nil {
			err = cmdRestore(c, pos[0], *at, *to, *yes)
		}
	case "history":
		pos := parse(fs, args)
		var c Config
		if len(pos) != 1 {
			err = &cliError{exitNeedsYes, "usage: " + profileCommand() + " history <path> [--json]"}
		} else if c, err = loadConfig(); err == nil {
			err = cmdHistory(c, expand(pos[0]), *asJSON)
		}
	case "diff":
		path := fs.String("path", "", "compare only this file or folder")
		metadata := fs.Bool("metadata", false, "include permissions and other metadata changes")
		pos := parse(fs, args)
		var c Config
		if len(pos) < 1 || len(pos) > 2 {
			err = &cliError{exitNeedsYes, "usage: " + profileCommand() + " diff <from> [<to>] [--path P] [--metadata] [--json]"}
		} else if c, err = loadConfig(); err == nil {
			to := "latest"
			if len(pos) == 2 {
				to = pos[1]
			}
			err = cmdDiff(c, pos[0], to, *path, *metadata, *asJSON)
		}
	case "recover":
		at := fs.String("at", "latest", "which saved version to recover")
		to := fs.String("to", "", "exact new destination; default is a private temporary directory")
		pos := parse(fs, args)
		hasYes := false
		fs.Visit(func(f *flag.Flag) { hasYes = hasYes || f.Name == "y" })
		var c Config
		if len(pos) != 1 || hasYes {
			err = &cliError{exitNeedsYes, "usage: " + profileCommand() + " recover <path> [--at A] [--to T] [--json] (never overwrites; no -y)"}
		} else if c, err = loadConfig(); err == nil {
			err = cmdRecover(c, pos[0], *at, *to, *asJSON)
		}
	case "rebuild":
		host := fs.String("host", "", "rebuild from this machine's backups")
		from := fs.String("from", "", "the old home path inside the backup")
		noHome := fs.Bool("no-home", false, "skip the home restore")
		noPackages := fs.Bool("no-packages", false, "skip /etc staging and package steps")
		at := fs.String("at", "latest", "source version for mapped paths")
		var mappings []string
		fs.Func("map", "restore absolute OLD=NEW (repeatable; NEW is the exact destination)", func(value string) error { mappings = append(mappings, value); return nil })
		pos := parse(fs, args)
		extended := false
		fs.Visit(func(f *flag.Flag) {
			extended = extended || f.Name == "map" || f.Name == "no-home" || f.Name == "no-packages" || f.Name == "at"
		})
		var c Config
		if len(pos) != 0 {
			err = &cliError{exitNeedsYes, "rebuild does not accept positional arguments"}
		} else if c, err = loadConfig(); err == nil {
			if extended || c.backupScope() == scopeDataOnly {
				err = cmdRebuildMapped(c, rebuildOptions{Host: *host, From: *from, At: *at, Maps: mappings, NoHome: *noHome, NoPackages: *noPackages, Yes: *yes})
			} else {
				err = cmdRebuild(c, *host, *from, *yes)
			}
		}
	case "set": // no flag parsing: "-PATH" removes from a list
		pos := slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--json" || a == "-json" })
		var c Config
		if c, err = loadConfig(); err == nil {
			err = cmdSet(c, pos, len(pos) != len(args))
		}
	default:
		fmt.Fprint(os.Stderr, usage)
		return exitNeedsYes
	}
	return reportError(err)
}

func reportError(err error) int {
	if err == nil {
		return 0
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
	return code
}

func cmdCloudSetup() error {
	bin, err := tool("rclone")
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "config") // #nosec G204 -- our rclone binary, fixed configuration command
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rclone configuration failed: rerun `%s cloud setup`", profileCommand())
	}
	return nil
}

func cmdInit(target string, password bool, paths []string, yes bool, dataOnly *bool) error {
	c, err := loadConfig()
	existing := err == nil
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err // invalid existing settings must not be silently replaced
		}
		c = defaultConfig()
	}
	currentHost, err := os.Hostname()
	if err != nil || currentHost == "" {
		return fmt.Errorf("cannot determine the current machine's host name")
	}
	if existing && c.Host != currentHost {
		message := "Reinitialize host " + clean(c.Host) + " as " + clean(currentHost) + "?"
		if !yes {
			if err := confirm(message, "Future backups and ordinary history use the current host. Old-host versions remain in this repository; a rebuild with --host can select them again. Saved paths, settings and password are retained.", "Reinitialize"); err != nil {
				return err
			}
		}
		note(message + " Old-host versions remain saved; default history will use the current host.")
	}
	c.Host = currentHost // in memory only until repository checks and atomic save succeed
	if err := c.initScope(dataOnly, paths, existing); err != nil {
		return &cliError{exitNeedsYes, err.Error()}
	}
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
	}
	if err := checkProfileRepository(repo); err != nil {
		return err
	}
	if c.Repo != "" && repositoryIdentity(c.Repo) != repositoryIdentity(repo) && !yes {
		if err := confirm("Change the repository for profile "+selectedProfile+"?",
			"Future runs and retention use the new repository. Existing snapshots stay in the old repository. The password and other settings are reused. Use a new profile to keep both destinations scheduled.", "Change it"); err != nil {
			return err
		}
	}
	if !filepath.IsAbs(repo) && !password && !strings.HasPrefix(repo, "rest:") {
		if _, err := os.Stat(passwordFile()); os.IsNotExist(err) {
			warn("no password: anyone who can read " + clean(redact(target)) + " can read your files (use --password for cloud targets)")
		}
	}
	c.Target, c.Repo, c.Since = target, repo, time.Now()
	created, shown := false, ""
	saved := false
	defer func() {
		if created && !saved {
			os.Remove(passwordFile())
		}
	}()
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
			return fmt.Errorf("this repository has a password: put it in %s and rerun with --password", passwordFile())
		}
	default:
		err = fmt.Errorf("can't reach %s: %w", redact(target), err)
	}
	if err != nil {
		if strings.HasPrefix(repo, "rclone:") && codeOf(err) != exitBadPass {
			return fmt.Errorf("can't reach the rclone destination: check your remote and credentials with `%s cloud setup`, then rerun `%s init`", profileCommand(), profileCommand())
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
	saved = true
	if err := applySchedule(c); err != nil {
		warn(err.Error() + " (scheduling unavailable; run `" + profileCommand() + " snapshot` or `" + profileCommand() + " backup` by hand)")
	} else {
		if hint := enableLinger(); hint != "" {
			warn(hint)
		}
		if err := startJob("snapshot"); err == nil {
			note("first snapshot running in the background: watch it with `" + profileCommand() + "`")
		}
	}
	lipgloss.Println(renderSettings(c))
	return nil
}

// setupPassword makes sure the password file exists: an existing one is used as is (that's how you
// connect to a passworded repo), otherwise it's typed (TTY) or generated. shown is a generated password.
func setupPassword() (created bool, shown string, err error) {
	if err := checkProfileDirectory(selectedProfile); err != nil {
		return false, "", err
	}
	if _, err := os.Stat(passwordFile()); err == nil {
		return false, "", nil
	} else if !os.IsNotExist(err) {
		return false, "", err
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
		if _, err := rand.Read(b); err != nil {
			return false, "", err
		}
		pw = base64.RawURLEncoding.EncodeToString(b)
		shown = pw
	}
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return false, "", err
	}
	f, err := os.OpenFile(passwordFile(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, "", err
	}
	_, writeErr := f.WriteString(pw + "\n")
	closeErr := f.Close()
	return true, shown, errors.Join(writeErr, closeErr)
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
		return &cliError{exitNeedsYes, "usage: " + profileCommand() + " set <key> <value>  (keys: " + strings.Join(setKeys, ", ") + ")"}
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
		line := fmt.Sprintf("  %s  %-11s %-8s %s", v.Time.Local().Format("2006-01-02 15:04"), v.Kind, short(v.Snapshot), dim.Render(ago(v.Time)))
		if !v.Mtime.IsZero() {
			line += dim.Render(fmt.Sprintf("  · %d bytes, modified %s", v.Size, v.Mtime.Local().Format("2006-01-02 15:04")))
		}
		lipgloss.Println(line)
	}
	lipgloss.Println(dim.Render("  restore one: " + profileCommand() + " restore " + tildify(p) + " --at <snapshot>"))
	return nil
}

func renderSettings(c Config) string {
	row := func(k, v string) string { return "  " + dim.Render(fmt.Sprintf("%-15s", k)) + v }
	lines := []string{accent.Render("cairn") + dim.Render("  profile "+selectedProfile) + "  " + clean(redact(c.Target)) + dim.Render("  host "+clean(c.Host))}
	scope := "machine (system paths and rebuild manifests)"
	if c.backupScope() == scopeDataOnly {
		scope = "data-only (configured paths; no machine rebuild)"
	}
	lines = append(lines, row("backup.scope", scope))
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
	lines := []string{accent.Render("cairn") + dim.Render("  profile "+clean(st.Profile)) + "  " + clean(st.Target) + "  " + online + dim.Render("  host "+clean(st.Host)), ""}
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
		lines = append(lines, "", amber.Render("! ")+dim.Render("systemd user timers unavailable: run "+profileCommand()+" snapshot/backup yourself"))
	}
	return strings.Join(lines, "\n")
}
