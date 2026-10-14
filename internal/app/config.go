package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Job struct {
	Every string   `json:"every"`
	Keep  string   `json:"keep"`
	Paths []string `json:"paths"`
}

type Config struct {
	Target      string    `json:"target"` // as the user typed it
	Repo        string    `json:"repo"`   // what restic gets as the repository
	Host        string    `json:"host"`
	BackupScope string    `json:"backup_scope"`
	Since       time.Time `json:"since,omitzero"` // when init ran here; rebuild only trusts older backups
	Snapshot    Job       `json:"snapshot"`
	Backup      Job       `json:"backup"`
	Exclude     []string  `json:"exclude,omitempty"`
}

const (
	scopeMachine  = "machine"
	scopeDataOnly = "data-only"
)

func (c Config) backupScope() string {
	if c.BackupScope == "" {
		return scopeMachine
	}
	return c.BackupScope
}

// initScope preserves settings unless sources or scope are explicitly supplied.
// Changing scope needs fresh sources so old automatic system paths cannot leak in.
func (c *Config) initScope(dataOnly *bool, paths []string, existing bool) error {
	scope := c.backupScope()
	if dataOnly != nil {
		wanted := scopeMachine
		if *dataOnly {
			wanted = scopeDataOnly
		}
		if existing && wanted != scope && len(paths) == 0 {
			return fmt.Errorf("changing backup scope requires --path for the complete new source list")
		}
		scope = wanted
	}
	c.BackupScope = scope
	if len(paths) == 0 && !existing && scope == scopeDataOnly {
		paths = []string{home}
	}
	if len(paths) > 0 {
		c.Snapshot.Paths = slices.Clone(paths)
		c.Backup.Paths = slices.Clone(paths)
		if scope == scopeMachine {
			c.Backup.Paths = append(c.Backup.Paths, "/etc", "/usr/share/keyrings")
		}
	}
	return nil
}

// Regenerable stuff: anything a lockfile, dotfile or package manager can recreate.
var builtinExcludes = []string{
	".venv", "venv", "__pycache__", "node_modules", ".cache", ".npm", ".pnpm-store", ".yarn/cache",
	".cargo/registry", ".cargo/git", ".rustup", ".gradle/caches", ".m2/repository", "go/pkg/mod",
	".bun/install/cache", ".tox", ".nox", ".mypy_cache", ".pytest_cache", ".ruff_cache",
	".ipynb_checkpoints", ".conda/pkgs", ".nvm/versions", ".pyenv/versions", ".var/app/*/cache",
	".local/share/Trash", ".local/share/containers", ".local/share/docker", ".local/share/flatpak",
	".local/share/Steam/steamapps", ".local/share/uv/tools", ".local/share/uv/python", ".local/share/pipx/venvs",
}

var home, _ = os.UserHomeDir()

func configRoot() string   { d, _ := os.UserConfigDir(); return filepath.Join(d, "cairn") }
func configDir() string    { return profileDir(configRoot(), selectedProfile) }
func configFile() string   { return filepath.Join(configDir(), "config.json") }
func passwordFile() string { return filepath.Join(configDir(), "password") }

func stateRoot() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "cairn")
	}
	return filepath.Join(home, ".local", "state", "cairn")
}

func stateDir() string { return profileDir(stateRoot(), selectedProfile) }

func dataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "cairn")
	}
	return filepath.Join(home, ".local", "share", "cairn")
}

func defaultConfig() Config {
	host, _ := os.Hostname()
	return Config{
		Host:        host,
		BackupScope: scopeMachine,
		Snapshot:    Job{Every: "1h", Keep: "24h,7d,4w", Paths: []string{home}},
		Backup:      Job{Every: "daily", Keep: "7d,4w,12m", Paths: []string{home, "/etc", "/usr/share/keyrings"}},
	}
}

func loadConfig() (Config, error) {
	c, err := loadProfile(selectedProfile)
	if os.IsNotExist(err) {
		return c, fmt.Errorf("profile %s is not set up yet: run `%s init <target>`: %w", selectedProfile, profileCommand(), err)
	}
	return c, err
}

func loadProfile(name string) (Config, error) {
	var c Config
	if err := checkProfileDirectory(name); err != nil {
		return c, err
	}
	b, err := os.ReadFile(filepath.Join(profileDir(configRoot(), name), "config.json"))
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("invalid configuration for profile %s: %w", name, err)
	}
	c.BackupScope = c.backupScope()
	if err := c.validate(); err != nil {
		return c, fmt.Errorf("invalid configuration for profile %s: %w", name, err)
	}
	return c, nil
}

func (c Config) validate() error {
	if c.backupScope() != scopeMachine && c.backupScope() != scopeDataOnly {
		return fmt.Errorf("unknown backup scope %q", c.BackupScope)
	}
	if c.Target == "" || c.Repo == "" || c.Host == "" {
		return fmt.Errorf("target, repo and host must be present")
	}
	if !filepath.IsAbs(c.Repo) && !slices.ContainsFunc(resticSchemes, func(scheme string) bool { return strings.HasPrefix(c.Repo, scheme) }) {
		return fmt.Errorf("repo must be an absolute local path or supported repository URL")
	}
	for _, kind := range []string{"snapshot", "backup"} {
		j := c.job(kind)
		if _, err := normEvery(j.Every); err != nil {
			return err
		}
		if _, err := keepFlags(j.Keep); err != nil {
			return err
		}
		if len(j.Paths) == 0 {
			return fmt.Errorf("%s needs at least one path", kind)
		}
		for _, path := range j.Paths {
			if !filepath.IsAbs(path) {
				return fmt.Errorf("%s source paths must be absolute", kind)
			}
		}
	}
	return nil
}

func (c Config) save() error {
	if err := checkProfileDirectory(selectedProfile); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(configDir(), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), configFile())
}

func (c *Config) job(kind string) *Job {
	if kind == "backup" {
		return &c.Backup
	}
	return &c.Snapshot
}

// schedules maps every-values to systemd OnCalendar ("" = timer off) and the expected gap between runs.
var schedules = map[string]struct {
	cal string
	iv  time.Duration
}{
	"15m": {"*:0/15", 15 * time.Minute}, "30m": {"*:0/30", 30 * time.Minute}, "1h": {"hourly", time.Hour},
	"6h": {"0/6:00", 6 * time.Hour}, "12h": {"0/12:00", 12 * time.Hour}, "daily": {"daily", 24 * time.Hour},
	"weekly": {"weekly", 7 * 24 * time.Hour}, "off": {"", 0},
}

var (
	everyChoices = []string{"15m", "30m", "1h", "6h", "12h", "daily", "weekly", "off"}
	everyAliases = map[string]string{"hourly": "1h", "1d": "daily", "24h": "daily", "1w": "weekly"}
)

func normEvery(every string) (string, error) {
	if a, ok := everyAliases[every]; ok {
		every = a
	}
	if _, ok := schedules[every]; !ok {
		return "", fmt.Errorf("unknown frequency %q (use one of: %s)", every, strings.Join(everyChoices, ", "))
	}
	return every, nil
}

func onCalendar(every string) (string, error) {
	every, err := normEvery(every)
	return schedules[every].cal, err
}

// interval is how often a job should succeed; used to flag overdue jobs.
func interval(every string) time.Duration { return schedules[every].iv }

var keepUnits = map[byte]string{'h': "--keep-hourly", 'd': "--keep-daily", 'w': "--keep-weekly", 'm': "--keep-monthly", 'y': "--keep-yearly"}

// keepFlags turns "8,24h,7d,4w" into restic forget flags (a bare number = keep the last N).
func keepFlags(keep string) ([]string, error) {
	var out []string
	for part := range strings.SplitSeq(keep, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && n > 0 {
			out = append(out, "--keep-last", part)
			continue
		}
		flag, ok := keepUnits[part[len(part)-1]]
		n, err := strconv.Atoi(part[:len(part)-1])
		if !ok || err != nil || n < 1 {
			return nil, fmt.Errorf("bad retention %q: use e.g. 24h,7d,4w,12m,2y (a bare number keeps the last N)", part)
		}
		out = append(out, flag, strconv.Itoa(n))
	}
	if out == nil {
		return nil, fmt.Errorf("retention is empty: use e.g. 24h,7d,4w")
	}
	return out, nil
}

func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[1:])
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// editList applies "+x" (add), "-x" (remove) or "a,b" (replace) to a list.
func editList(list []string, value string, norm func(string) string) []string {
	switch {
	case strings.HasPrefix(value, "+"):
		v := norm(value[1:])
		if !slices.Contains(list, v) {
			list = append(list, v)
		}
		return list
	case strings.HasPrefix(value, "-"):
		v := norm(value[1:])
		return slices.DeleteFunc(slices.Clone(list), func(s string) bool { return s == v })
	}
	var out []string
	for v := range strings.SplitSeq(value, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, norm(v))
		}
	}
	return out
}

var setKeys = []string{"snapshot.every", "snapshot.keep", "snapshot.paths", "backup.every", "backup.keep", "backup.paths", "exclude"}

// set changes one key; the caller re-applies timers when an every-key changed.
func (c *Config) set(key, value string) error {
	kind, field, _ := strings.Cut(key, ".")
	if key == "exclude" {
		c.Exclude = editList(c.Exclude, value, func(s string) string { return s })
		return nil
	}
	if kind != "snapshot" && kind != "backup" {
		return fmt.Errorf("unknown key %q (keys: %s)", key, strings.Join(setKeys, ", "))
	}
	j := c.job(kind)
	switch field {
	case "every":
		every, err := normEvery(value)
		if err != nil {
			return err
		}
		j.Every = every
	case "keep":
		if _, err := keepFlags(value); err != nil {
			return err
		}
		j.Keep = value
	case "paths":
		j.Paths = editList(j.Paths, value, expand)
		if len(j.Paths) == 0 {
			return fmt.Errorf("%s needs at least one path", key)
		}
	default:
		return fmt.Errorf("unknown key %q (keys: %s)", key, strings.Join(setKeys, ", "))
	}
	return nil
}

// excludeArgs builds restic --exclude flags: builtin + user + the repo/rebuild dirs themselves.
func (c Config) excludeArgs() ([]string, error) {
	profiles, err := configuredProfiles()
	if err != nil {
		return nil, err
	}
	ex := slices.Concat(builtinExcludes, c.Exclude, []string{globEscape(filepath.Join(stateDir(), "rebuild"))})
	names := []string{"default", selectedProfile}
	for name := range profiles {
		names = append(names, name)
	}
	for _, name := range names {
		ex = append(ex, globEscape(filepath.Join(profileDir(stateRoot(), name), "rebuild")))
		if c.backupScope() == scopeDataOnly {
			ex = append(ex, globEscape(filepath.Join(profileDir(stateRoot(), name), "manifests")))
		}
	}
	if c.backupScope() == scopeDataOnly {
		// Include stale state whose profile configuration is no longer present.
		ex = append(ex, globEscape(filepath.Join(stateRoot(), "profiles"))+"/*/manifests")
	}
	for _, repo := range localRepositories(c, profiles) {
		ex = append(ex, globEscape(repo))
	}
	out := []string{"--exclude-caches"}
	for _, e := range ex {
		out = append(out, "--exclude", e)
	}
	return out, nil
}
