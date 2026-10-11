package app

import (
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
