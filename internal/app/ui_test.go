package app

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestDashboardReportsBackupProblems(t *testing.T) {
	now := time.Now()
	job := JobStatus{Every: "1h", Keep: "24h,7d,4w", Count: 4, Last: &now, LastID: "1234abcd"}
	for _, tc := range []struct {
		name, want string
		online     bool
		result     string
		stale      bool
	}{
		{"offline", "OFFLINE", false, "ok", false},
		{"failed and overdue", "FAILED", true, "failed", true},
		{"partial", "PARTIAL", true, "partial", false},
		{"overdue", "OVERDUE", true, "ok", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := job
			j.Result, j.Stale = tc.result, tc.stale
			st := Status{Target: "/mnt/backup/cairn", Online: tc.online, Scheduler: true, Snapshot: j, Backup: j}
			view := ansi.Strip(renderDashboard(st, 104, 32))
			if !strings.Contains(view, tc.want) || strings.Contains(view, "✓ OK") {
				t.Fatalf("backup problem misreported:\n%s", view)
			}
			if !tc.online && strings.Contains(view, j.LastID) {
				t.Fatal("offline target must not imply current history is available")
			}
		})
	}
}

func TestStorageGuidesPreserveDestinationAndBackNavigation(t *testing.T) {
	m := model{w: 80, h: 24, list: newUIList(), c: Config{Target: "/mnt/backup/cairn", Repo: "/mnt/backup/cairn"}}
	m = m.showTargets()
	m.list.Select(2)
	press := func(code rune, text string) {
		t.Helper()
		next, _ := m.Update(tea.KeyPressMsg{Code: code, Text: text})
		m = next.(model)
	}
	press(tea.KeyEnter, "")
	if m.scr != storageDetails || !strings.Contains(ansi.Strip(m.View().Content), "gdrive:backups") {
		t.Fatal("Drive selection must show setup commands")
	}
	press('?', "?")
	if m.scr != helpScreen {
		t.Fatal("Help must open from a storage guide")
	}
	press(tea.KeyEscape, "")
	if m.scr != storageDetails || !strings.Contains(ansi.Strip(m.View().Content), "gdrive:backups") {
		t.Fatal("Back must restore the selected storage guide")
	}
	press(tea.KeyEscape, "")
	if m.scr != storageList {
		t.Fatal("Back from setup must return to the destinations")
	}
	press(tea.KeyEscape, "")
	if m.scr != dash || m.c.Target != "/mnt/backup/cairn" || m.c.Repo != m.c.Target {
		t.Fatal("Browsing setup guides must leave the current target unchanged")
	}
}

func TestGuidesFitSmallTerminals(t *testing.T) {
	for _, width := range []int{40, 60, 80, 110} {
		for _, scr := range []screen{storageList, storageDetails, helpScreen} {
			m := model{w: width, h: 24, list: newUIList(), destination: storageChoices[2]}
			if scr == storageList {
				m = m.showTargets()
			} else {
				m = m.showGuide(scr)
			}
			view := m.View().Content
			if lipgloss.Width(view) > width || lipgloss.Height(view) > m.h {
				t.Errorf("guide %d exceeds %dx%d: %dx%d", scr, width, m.h, lipgloss.Width(view), lipgloss.Height(view))
			}
		}
	}
}

func TestDashboardFitsTerminalWidth(t *testing.T) {
	now := time.Now()
	job := JobStatus{Every: "daily", Keep: "7d,4w,12m", Count: 21, Last: &now, Next: &now, LastID: "1234abcd"}
	st := Status{Target: "/mnt/" + strings.Repeat("long-backup-path/", 12), Online: true, Scheduler: true, Snapshot: job, Backup: job}
	for _, width := range []int{40, 60, 80, 110, 160} {
		view := renderDashboard(st, uiWidth(width), 32)
		if lipgloss.Width(view) > width-4 {
			t.Errorf("dashboard exceeds %d columns: got %d", width, lipgloss.Width(view))
		}
		text := ansi.Strip(view)
		if !strings.Contains(text, "SNAPSHOTS") || !strings.Contains(text, "FULL BACKUPS") {
			t.Errorf("a backup kind disappeared at width %d", width)
		}
		if !strings.Contains(text, "● ONLINE") || strings.Count(text, "✓ OK") != 2 {
			t.Errorf("status badges wrapped at width %d:\n%s", width, text)
		}
	}
}

func TestTUIKeepsControlsVisible(t *testing.T) {
	now := time.Now()
	job := JobStatus{Every: "daily", Keep: "7d,4w,12m", Count: 21, Last: &now, Next: &now, LastID: "1234abcd"}
	for _, width := range []int{40, 60, 80, 110} {
		for _, scr := range []screen{dash, snapList, dirList, confirmRestore} {
			m := model{
				w: width, h: 24, scr: scr, loaded: true, list: newUIList(),
				c:    Config{Host: "demo-laptop"},
				st:   Status{Target: "/mnt/backup/cairn", Online: true, Scheduler: true, Snapshot: job, Backup: job},
				snap: Snap{ShortID: "1234abcd", Time: now}, target: Node{Path: "/home/demo/projects/notes.md"},
			}
			title := "Saved versions"
			if scr == dirList {
				title = "1234abcd · /home/demo/" + strings.Repeat("long-backup-path/", 12)
			}
			m.setList(title, []list.Item{
				item{title: "snapshot", desc: strings.Repeat("long-backup-path/", 12)},
				item{title: "backup", desc: "a saved version"},
			})
			view := m.View().Content
			if lipgloss.Width(view) > width || lipgloss.Height(view) > m.h {
				t.Errorf("screen %d overflows %dx%d terminal: %dx%d\n%s", scr, width, m.h, lipgloss.Width(view), lipgloss.Height(view), ansi.Strip(view))
			}
		}
	}
}
