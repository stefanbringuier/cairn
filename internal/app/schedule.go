package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func unitDir() string { d, _ := os.UserConfigDir(); return filepath.Join(d, "systemd", "user") }

func systemctl(args ...string) ([]byte, error) {
	if os.Getenv("XDG_RUNTIME_DIR") == "" { // e.g. under su; systemctl --user needs it
		os.Setenv("XDG_RUNTIME_DIR", fmt.Sprintf("/run/user/%d", os.Getuid()))
	}
	return exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput() // #nosec G204 -- fixed binary
}

const serviceUnit = `[Unit]
Description=cairn %[1]s

[Service]
Type=oneshot
ExecStart="%[2]s" --profile "%[5]s" %[1]s
Environment="PATH=%[3]s"
%[6]s
Nice=19
IOSchedulingClass=idle
CPUQuota=%[4]d%%
SuccessExitStatus=3
`

const timerUnit = `[Unit]
Description=cairn %[1]s schedule

[Timer]
OnCalendar=%[2]s
Persistent=true

[Install]
WantedBy=timers.target
`

// unitSafe rejects what systemd would reinterpret inside a quoted value (specifiers, env vars, escapes, newlines).
func unitSafe(s string) bool { return clean(s) == s && !strings.ContainsAny(s, "\"\\%$") }

// Explicit roots keep a timer on the same configuration as the invoking CLI.
func scheduleEnvironment() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	roots := []struct{ key, value string }{
		{"XDG_CONFIG_HOME", filepath.Dir(configRoot())},
		{"XDG_STATE_HOME", filepath.Dir(stateRoot())},
		{"XDG_DATA_HOME", filepath.Dir(dataDir())},
		{"XDG_CACHE_HOME", cache},
	}
	var out strings.Builder
	for _, root := range roots {
		if !filepath.IsAbs(root.value) || !unitSafe(root.value) {
			return "", fmt.Errorf("can't schedule: %s needs an absolute path without quotes, %%, $, backslashes or control characters", root.key)
		}
		fmt.Fprintf(&out, "Environment=\"%s=%s\"\n", root.key, root.value)
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

// applySchedule writes both services and turns each timer on or off to match the config.
func applySchedule(c Config) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if !unitSafe(self) {
		return fmt.Errorf("can't schedule %q: move cairn to a path without quotes, %%, $ or backslashes", self)
	}
	environment, err := scheduleEnvironment()
	if err != nil {
		return err
	}
	for _, kind := range []string{"snapshot", "backup"} {
		if _, err := onCalendar(c.job(kind).Every); err != nil {
			return err
		}
	}
	// Timers get the user's PATH so manifests find ~/.cargo/bin, ~/.local/bin, brew, nvm...
	path := os.Getenv("PATH")
	if !unitSafe(path) {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	if err := os.MkdirAll(unitDir(), 0o755); err != nil { // #nosec G301 -- systemd's own dir layout
		return err
	}
	quota := max(50, runtime.NumCPU()*50) // half the machine: some laptops hang at full load
	for _, kind := range []string{"snapshot", "backup"} {
		cal, err := onCalendar(c.job(kind).Every)
		if err != nil {
			return err
		}
		unit := filepath.Join(unitDir(), unitName(kind))
		if err := os.WriteFile(unit+".service", fmt.Appendf(nil, serviceUnit, kind, self, path, quota, selectedProfile, environment), 0o600); err != nil {
			return err
		}
		if cal == "" {
			_, _ = systemctl("disable", "--now", unitName(kind)+".timer") // fine if it was never enabled
			os.Remove(unit + ".timer")
			continue
		}
		if err := os.WriteFile(unit+".timer", fmt.Appendf(nil, timerUnit, kind, cal), 0o600); err != nil {
			return err
		}
	}
	if out, err := systemctl("daemon-reload"); err != nil {
		return fmt.Errorf("systemctl --user daemon-reload: %s", strings.TrimSpace(string(out)))
	}
	for _, kind := range []string{"snapshot", "backup"} {
		timer, want, verb := unitName(kind)+".timer", "active", "start"
		if c.job(kind).Every == "off" {
			want, verb = "inactive", "stop"
		} else if out, err := systemctl("enable", "--now", timer); err != nil {
			return fmt.Errorf("enable %s: %s", timer, strings.TrimSpace(string(out)))
		}
		// Where systemctl thinks it's in a chroot (some sandboxes and containers) it exits 0
		// after only writing enable symlinks, so check what the user manager actually runs.
		if out, _ := systemctl("show", "-p", "ActiveState", "--value", timer); strings.TrimSpace(string(out)) != want {
			return fmt.Errorf("systemd didn't %s %s (%s)", verb, timer, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// startJob kicks off a run in the background through its service.
func startJob(kind string) error {
	if out, err := systemctl("start", "--no-block", unitName(kind)+".service"); err != nil {
		return fmt.Errorf("start %s: %s", unitName(kind), strings.TrimSpace(string(out)))
	}
	return nil
}

// enableLinger lets timers run while logged out (allowed for yourself without sudo on most distros).
func enableLinger() string {
	if out, err := exec.Command("loginctl", "enable-linger").CombinedOutput(); err != nil {
		return fmt.Sprintf("timers only run while you're logged in; to fix: sudo loginctl enable-linger %s (%s)",
			os.Getenv("USER"), strings.TrimSpace(string(out)))
	}
	return ""
}

type unitState struct {
	Next    time.Time
	Running bool
	Result  string // "ok", "partial", "failed", "" = never ran via systemd
}

func readUnits(kind string) (u unitState, ok bool) {
	out, err := systemctl("show", "--timestamp=unix", unitName(kind)+".timer", unitName(kind)+".service",
		"-p", "NextElapseUSecRealtime", "-p", "ActiveState", "-p", "Result", "-p", "ExecMainStatus", "-p", "ExecMainExitTimestamp")
	if err != nil {
		return u, false
	}
	// Output is the timer's properties, a blank line, then the service's.
	timer, service, _ := strings.Cut(string(out), "\n\n")
	props := func(s string) map[string]string {
		m := map[string]string{}
		for line := range strings.SplitSeq(s, "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				m[k] = v
			}
		}
		return m
	}
	t, s := props(timer), props(service)
	u.Next = unixTime(t["NextElapseUSecRealtime"])
	u.Running = s["ActiveState"] == "activating"
	if !unixTime(s["ExecMainExitTimestamp"]).IsZero() {
		switch {
		case s["ExecMainStatus"] == "3":
			u.Result = "partial"
		case s["Result"] == "success":
			u.Result = "ok"
		default:
			u.Result = "failed"
		}
	}
	return u, true
}

func unixTime(v string) time.Time {
	sec, err := strconv.ParseInt(strings.TrimPrefix(v, "@"), 10, 64)
	if err != nil || sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// lockJobs serializes snapshot and backup runs (both fire at 00:00).
func lockJobs(tty bool) (release func(), err error) {
	if err := os.MkdirAll(stateRoot(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(stateRoot(), "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		if tty {
			note("waiting for the other cairn job to finish…")
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			f.Close()
			return nil, err
		}
	}
	return func() { f.Close() }, nil
}
