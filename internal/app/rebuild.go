package app

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

func manifestDir() string { return filepath.Join(stateDir(), "manifests") }

// A manager lists what's installed (as names) and reinstalls it with install+names.
type manager struct {
	file    string
	list    []string
	names   func(out string) []string
	install []string
	slash   bool // names may contain "/" (npm scopes); elsewhere "/" would mean a local file to install
}

// firstWords takes each line's first word, skipping indented and "- " lines (cargo/uv sub-entries) and drop.
func firstWords(drop ...string) func(string) []string {
	return func(out string) []string {
		var names []string
		for line := range strings.SplitSeq(out, "\n") {
			f := strings.Fields(line)
			if len(f) == 0 || line[0] == ' ' || line[0] == '\t' || line[0] == '-' || slices.Contains(drop, f[0]) {
				continue
			}
			names = append(names, strings.TrimSuffix(f[0], ":"))
		}
		return names
	}
}

func npmNames(out string) []string {
	var v struct{ Dependencies map[string]any }
	_ = json.Unmarshal([]byte(out), &v)
	delete(v.Dependencies, "npm")
	delete(v.Dependencies, "corepack")
	return slices.Sorted(maps.Keys(v.Dependencies))
}

var managers = []manager{
	{"apt.txt", []string{"apt-mark", "showmanual"}, firstWords(), []string{"sudo", "apt", "install", "-y"}, false},
	{"snap.txt", []string{"snap", "list"}, firstWords("Name", "core", "core18", "core20", "core22", "core24", "snapd", "bare"), []string{"sudo", "snap", "install"}, false},
	{"flatpak.txt", []string{"flatpak", "list", "--app", "--columns=application"}, firstWords(), []string{"flatpak", "install", "-y"}, false},
	{"brew.txt", []string{"brew", "leaves"}, firstWords(), []string{"brew", "install"}, true},
	{"uv-tools.txt", []string{"uv", "tool", "list"}, firstWords(), []string{"sh", "-c", `for t in "$@"; do uv tool install "$t"; done`, "uv"}, false},
	{"pipx.txt", []string{"pipx", "list", "--short"}, firstWords(), []string{"sh", "-c", `for t in "$@"; do pipx install "$t"; done`, "pipx"}, false},
	{"cargo.txt", []string{"cargo", "install", "--list"}, firstWords(), []string{"cargo", "install"}, false},
	{"npm.txt", []string{"npm", "ls", "-g", "--depth=0", "--json"}, npmNames, []string{"npm", "install", "-g"}, true},
}

// writeManifests records what's installed, one name per line, for every manager present.
func writeManifests() {
	_ = os.MkdirAll(manifestDir(), 0o700)
	for _, m := range managers {
		out, err := exec.Command(m.list[0], m.list[1:]...).Output() // #nosec G204 -- fixed manager commands
		if err != nil {
			continue // not installed here
		}
		_ = os.WriteFile(filepath.Join(manifestDir(), m.file), []byte(strings.Join(m.names(string(out)), "\n")+"\n"), 0o600)
	}
	if out, err := exec.Command("crontab", "-l").Output(); err == nil {
		_ = os.WriteFile(filepath.Join(manifestDir(), "crontab.txt"), out, 0o600)
	}
}

var validName = regexp.MustCompile(`^[A-Za-z0-9@][A-Za-z0-9@._+:/=-]*$`)

// readNames loads a manifest restored from the repo. The names end up as arguments to
// `sudo apt install` & co, so anything that isn't a plain package name is dropped.
func readNames(m manager, dir string) []string {
	if dir == "" {
		return nil // no recipe: never fall back to live package lists
	}
	b, err := os.ReadFile(filepath.Join(dir, m.file)) // #nosec G304 -- fixed manager filename in the private restored recipe
	if err != nil {
		return nil
	}
	var names []string
	for n := range strings.FieldsSeq(string(b)) {
		if validName.MatchString(n) && (m.slash || !strings.Contains(n, "/")) {
			names = append(names, n)
		} else {
			warn(fmt.Sprintf("ignoring suspicious entry %q in %s", clean(n), m.file))
		}
	}
	return names
}

type step struct {
	title string
	cmd   func() (cmd []string, skip string) // built when it runs: apt's list is filtered after `apt update`
}

func cmdRebuild(c Config, host, from string, yes bool) error {
	if c.backupScope() == scopeDataOnly {
		return fmt.Errorf("data-only backups cannot rebuild a machine; use `%s history`, `%s recover`, or `%s restore` for files", profileCommand(), profileCommand(), profileCommand())
	}
	r, err := newRestic(c)
	if err != nil {
		return err
	}
	all, err := r.snapshots("")
	if err != nil {
		return err
	}
	// Only trust what existed before this machine was set up, so a fresh machine's own runs never win.
	all = rebuildCandidates(c, all)
	if host == "" {
		hosts := map[string]bool{}
		for _, s := range all {
			if s.kind() == "backup" {
				hosts[s.Hostname] = true
			}
		}
		switch {
		case hosts[c.Host]:
			host = c.Host
		case len(hosts) == 1:
			host = slices.Collect(maps.Keys(hosts))[0]
		case len(hosts) == 0:
			return fmt.Errorf("no backups in %s from before this machine was set up", redact(c.Target))
		default:
			return fmt.Errorf("backups from several machines here (%s): pick one with --host", strings.Join(slices.Sorted(maps.Keys(hosts)), ", "))
		}
	}
	var b Snap // newest backup: the recipe (manifests, /etc, which path was home)
	for _, s := range all {
		if s.Hostname == host && s.kind() == "backup" {
			b = s
		}
	}
	if b.ID == "" {
		return fmt.Errorf("no backups from host %s", host)
	}
	if from == "" { // restic sorts paths, so find the old home by the snapshot's user name
		for _, p := range b.Paths {
			if filepath.Base(p) == b.Username {
				from = p
			}
		}
		if from == "" {
			return fmt.Errorf("can't tell which path was the old home; pick one with --from (%s)", strings.Join(b.Paths, ", "))
		}
	}
	src := b // the home comes from the newest snapshot or backup that has it (hourly beats daily)
	for _, s := range all {
		if s.Hostname == host && s.kind() != "pre-restore" && s.covers(from) && s.Time.After(src.Time) {
			src = s
		}
	}
	if !yes {
		if err := confirm(fmt.Sprintf("Rebuild from %s's %s %s (%s)?", clean(host), src.kind(), src.ShortID, ago(src.Time)),
			fmt.Sprintf("Restores %s into %s (overwriting), stages /etc for reference, then walks through reinstalling packages.\n%s",
				tildify(from), home, dim.Render("Close desktop apps first: they write their settings back when they quit.")), "Rebuild"); err != nil {
			return err
		}
	}

	note("restoring " + tildify(from) + " → " + home + " …")
	if _, err := r.out("restore", "--target", home, "--", src.ID+":"+from); err != nil {
		return err
	}
	// The restored home brought the old cairn config: keep its settings (paths re-homed), but this target and host.
	if old, err := loadConfig(); err == nil && old.Repo == c.Repo {
		for _, j := range []*Job{&old.Snapshot, &old.Backup} {
			for i, p := range j.Paths {
				if p == from || strings.HasPrefix(p, from+"/") {
					j.Paths[i] = home + strings.TrimPrefix(p, from)
				}
			}
		}
		old.Target, old.Since = c.Target, c.Since
		c = old
	}
	c.Host = host
	if err := c.save(); err != nil {
		return err
	}
	if err := applySchedule(c); err != nil {
		warn(err.Error())
	}
	ok("home restored")
	return rebuildPackages(r, b, from, yes)
}

func rebuildPackages(r Restic, b Snap, from string, yes bool) error {
	return rebuildPackagesAt(r, b, from, yes, filepath.Join(stateDir(), "rebuild"))
}

func rebuildPackagesAt(r Restic, b Snap, from string, yes bool, stage string) error {
	var err error
	recipe := ""
	for _, p := range b.Paths {
		switch {
		case p == "/etc" || p == "/usr/share/keyrings":
			if _, err := r.out("restore", "--target", filepath.Join(stage, filepath.Base(p)), "--", b.ID+":"+p); err != nil {
				warn("couldn't stage " + p + ": " + err.Error())
			}
		case savedManifestPath(p): // explicit recipe source, including paths beneath the old home
			recipe, err = restoreRebuildManifests(r, b, p, stage)
			if err != nil {
				return fmt.Errorf("couldn't restore the package lists; no packages installed: %w", err)
			}
			defer func(dir string) { _ = os.RemoveAll(dir) }(recipe)
		case p != from:
			note("also in this backup: " + clean(p) + " → `" + profileCommand() + " restore " + clean(p) + " --at " + b.ShortID + "`")
		}
	}

	etc, keyrings := filepath.Join(stage, "etc", "apt"), filepath.Join(stage, "keyrings")
	var steps []step
	if _, err := os.Stat(etc); err == nil {
		steps = append(steps, step{"Bring back third-party apt sources and keys (existing files are kept)", func() ([]string, string) {
			return []string{"sudo", "sh", "-c", `cp -rn "$1/sources.list.d/." /etc/apt/sources.list.d/; ` +
				`[ -d "$1/keyrings" ] && mkdir -p /etc/apt/keyrings && cp -rn "$1/keyrings/." /etc/apt/keyrings/; ` +
				`[ -d "$1/trusted.gpg.d" ] && cp -rn "$1/trusted.gpg.d/." /etc/apt/trusted.gpg.d/; ` +
				`[ -d "$2" ] && cp -rn "$2/." /usr/share/keyrings/; true`, "sh", etc, keyrings}, ""
		}})
	}
	if names := readNames(managers[0], recipe); len(names) > 0 {
		steps = append(steps, step{"Refresh apt", func() ([]string, string) { return []string{"sudo", "apt", "update"}, "" }})
		steps = append(steps, step{fmt.Sprintf("Reinstall %d apt packages", len(names)), func() ([]string, string) {
			out, err := exec.Command("apt-cache", "pkgnames").Output()
			if err != nil || len(out) == 0 {
				return nil, "apt-cache returned no package names"
			}
			avail := map[string]bool{}
			for n := range strings.FieldsSeq(string(out)) {
				avail[n] = true
			}
			keep := slices.DeleteFunc(slices.Clone(names), func(n string) bool { return !avail[strings.Split(n, ":")[0]] })
			return append(slices.Clone(managers[0].install), keep...), ""
		}})
	}
	for _, m := range managers[1:] {
		names := readNames(m, recipe)
		if len(names) == 0 {
			continue
		}
		steps = append(steps, step{fmt.Sprintf("Reinstall %d %s packages", len(names), m.list[0]), func() ([]string, string) {
			if _, err := exec.LookPath(m.list[0]); err != nil { // checked now: an earlier step may have installed it
				return nil, m.list[0] + " isn't installed (install it, then rerun `" + profileCommand() + " rebuild`)"
			}
			return append(slices.Clone(m.install), names...), ""
		}})
	}
	if _, err := os.Stat(filepath.Join(recipe, "crontab.txt")); recipe != "" && err == nil {
		steps = append(steps, step{"Restore your crontab", func() ([]string, string) {
			return []string{"crontab", filepath.Join(recipe, "crontab.txt")}, ""
		}})
	}

	for _, st := range steps {
		cmd, skip := st.cmd()
		if cmd == nil {
			note("skipped " + st.title + ": " + skip)
			continue
		}
		if !yes {
			if err := confirm(st.title+"?", code.Render(strings.Join(cmd, " ")), "Run"); err != nil {
				note("skipped: " + st.title)
				continue
			}
		}
		x := exec.Command(cmd[0], cmd[1:]...) // #nosec G204 -- fixed commands + validated package names
		x.Stdin, x.Stdout, x.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := x.Run(); err != nil {
			warn(st.title + " failed: " + err.Error())
		} else {
			ok(st.title)
		}
	}
	ok("rebuild done · /etc from the old machine is in " + tildify(filepath.Join(stage, "etc")) + " for reference")
	return nil
}

func savedManifestPath(path string) bool {
	if strings.HasSuffix(path, "/cairn/manifests") {
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		return false
	}
	parts = parts[len(parts)-4:]
	return parts[0] == "cairn" && parts[1] == "profiles" && validProfile(parts[2]) && parts[2] != "default" && parts[3] == "manifests"
}

// Restore the chosen machine recipe into a fresh directory, even when its
// source was under the old home. Newer snapshots may omit the recipe entirely;
// existing live lists must neither replace nor supplement the chosen backup.
func restoreRebuildManifests(r Restic, backup Snap, source, stage string) (string, error) {
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(stage, "manifests-")
	if err != nil {
		return "", err
	}
	if _, err := r.out("restore", "--target", dir, "--", backup.ID+":"+source); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// Data-only backups cannot become a machine recipe or home source after a
// reconnect with machine scope. Legacy, untagged backups keep their old meaning.
func rebuildCandidates(c Config, all []Snap) []Snap {
	return slices.DeleteFunc(all, func(s Snap) bool {
		return slices.Contains(s.Tags, scopeDataOnly) || (!c.Since.IsZero() && !s.Time.Before(c.Since))
	})
}
