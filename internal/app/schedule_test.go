package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSchedules(t *testing.T) {
	for every, want := range map[string]string{"15m": "*:0/15", "1h": "hourly", "hourly": "hourly", "12h": "0/12:00", "daily": "daily", "1w": "weekly", "off": ""} {
		if got, err := onCalendar(every); err != nil || got != want {
			t.Errorf("%s: got %q %v", every, got, err)
		}
	}
	if _, err := onCalendar("7m"); err == nil {
		t.Error("7m should fail")
	}
	if interval("12h") != 12*time.Hour || interval("off") != 0 {
		t.Error("interval")
	}
	c := defaultConfig()
	if err := c.set("snapshot.every", "hourly"); err != nil || c.Snapshot.Every != "1h" {
		t.Errorf("aliases must normalize to a TUI choice, got %q", c.Snapshot.Every)
	}
}

func TestScheduleRefusesIgnoredSystemctl(t *testing.T) {
	root := profileFixture(t)
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	// systemctl in a chroot or sandbox: every command is ignored, yet exits 0.
	script := `#!/bin/sh
if [ "$2" = enable ]; then echo "Cannot start unit with --now when systemd is not running, ignoring."
else echo "Running in chroot, ignoring command '$2'"; fi
exit 0
`
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(script), 0o700); err != nil { // #nosec G306 -- executable isolated fixture
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	c := defaultConfig()
	if err := applySchedule(c); err == nil || !strings.Contains(err.Error(), "didn't start cairn-snapshot.timer") {
		t.Fatalf("ignored enable reported as scheduled: %v", err)
	}
	c.Snapshot.Every, c.Backup.Every = "off", "off"
	if err := applySchedule(c); err == nil || !strings.Contains(err.Error(), "didn't stop cairn-snapshot.timer") {
		t.Fatalf("ignored disable reported as stopped: %v", err)
	}
}
