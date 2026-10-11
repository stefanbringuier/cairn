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
	Target   string    `json:"target"` // as the user typed it
	Repo     string    `json:"repo"`   // what restic gets as the repository
	Host     string    `json:"host"`
	Since    time.Time `json:"since,omitzero"` // when init ran here; rebuild only trusts older backups
	Snapshot Job       `json:"snapshot"`
	Backup   Job       `json:"backup"`
	Exclude  []string  `json:"exclude,omitempty"`
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

func configDir() string    { d, _ := os.UserConfigDir(); return filepath.Join(d, "cairn") }
func configFile() string   { return filepath.Join(configDir(), "config.json") }
func passwordFile() string { return filepath.Join(configDir(), "password") }

func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "cairn")
	}
	return filepath.Join(home, ".local", "state", "cairn")
}

func dataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "cairn")
	}
	return filepath.Join(home, ".local", "share", "cairn")
}

func defaultConfig() Config {
	host, _ := os.Hostname()
	return Config{
		Host:     host,
		Snapshot: Job{Every: "1h", Keep: "24h,7d,4w", Paths: []string{home}},
		Backup:   Job{Every: "daily", Keep: "7d,4w,12m", Paths: []string{home, "/etc", "/usr/share/keyrings"}},
	}
}

func loadConfig() (Config, error) {
	var c Config
	b, err := os.ReadFile(configFile())
	if os.IsNotExist(err) {
		return c, fmt.Errorf("not set up yet: run `cairn init <target>`")
	}
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	return c, err
}

func (c Config) save() error {
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(configFile(), append(b, '\n'), 0o600)
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
func (c Config) excludeArgs() []string {
	ex := slices.Concat(builtinExcludes, c.Exclude, []string{globEscape(filepath.Join(stateDir(), "rebuild"))})
	if strings.HasPrefix(c.Repo, "/") {
		ex = append(ex, globEscape(c.Repo))
	}
	out := []string{"--exclude-caches"}
	for _, e := range ex {
		out = append(out, "--exclude", e)
	}
	return out
}
