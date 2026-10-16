package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestDashboardChoosesNamedProfileWithoutWritingDefault(t *testing.T) {
	root := profileFixture(t)
	personal := saveFixtureProfile(t, "personal", filepath.Join(root, "personal-repo"))
	saveFixtureProfile(t, "phin", filepath.Join(root, "phin-repo"))
	before, err := os.ReadFile(filepath.Join(configRoot(), "profiles/personal/config.json"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := dashboardConfig(func(profiles map[string]Config) (string, error) {
		if len(profiles) != 2 || profiles["personal"].Repo != personal.Repo {
			t.Fatalf("wrong inventory: %+v", profiles)
		}
		if selectedProfile != "default" {
			t.Fatal("selection changed before choosing")
		}
		return "personal", nil
	})
	if err != nil || selectedProfile != "personal" || c.Repo != personal.Repo {
		t.Fatalf("selected wrong profile: %+v %v", c, err)
	}
	if _, err := os.Stat(filepath.Join(configRoot(), "config.json")); !os.IsNotExist(err) {
		t.Fatal("chooser created default config")
	}
	after, err := os.ReadFile(filepath.Join(configRoot(), "profiles/personal/config.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("chooser changed existing configuration")
	}
}

func TestDashboardStartupKeepsMissingInvalidAndExplicitSelections(t *testing.T) {
	for _, kind := range []string{"none", "default", "invalid", "dangling", "cancel", "removed", "sole", "invalid-named", "linked-named"} {
		t.Run(kind, func(t *testing.T) {
			root := profileFixture(t)
			if kind != "none" {
				saveFixtureProfile(t, "personal", filepath.Join(root, "repo"))
			}
			switch kind {
			case "default":
				saveFixtureProfile(t, "default", filepath.Join(root, "default-repo"))
			case "invalid":
				if err := os.WriteFile(configFile(), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "dangling":
				if err := os.Symlink(filepath.Join(root, "missing"), configFile()); err != nil {
					t.Fatal(err)
				}
			case "invalid-named":
				if err := os.WriteFile(filepath.Join(configRoot(), "profiles/personal/config.json"), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "linked-named":
				if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(configRoot(), "profiles/other")); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			c, err := dashboardConfig(func(profiles map[string]Config) (string, error) {
				called = true
				if len(profiles) != 1 {
					t.Fatal("sole profile not offered explicitly")
				}
				if kind == "cancel" {
					return "", errCancelled
				}
				if kind == "removed" {
					if err := os.Remove(filepath.Join(configRoot(), "profiles/personal/config.json")); err != nil {
						t.Fatal(err)
					}
				}
				return "personal", nil
			})
			wantChoice := kind == "cancel" || kind == "removed" || kind == "sole"
			if called != wantChoice {
				t.Fatalf("chooser called=%v, want %v", called, wantChoice)
			}
			if kind == "default" || kind == "sole" {
				if err != nil || c.Repo == "" {
					t.Fatalf("valid profile failed: %v", err)
				}
			} else if err == nil || selectedProfile != "default" {
				t.Fatalf("failed startup changed selection: %q %v", selectedProfile, err)
			}
			if kind == "cancel" && !errors.Is(err, errCancelled) {
				t.Fatal("cancellation error lost")
			}
		})
	}
	profileFixture(t)
	saveFixtureProfile(t, "personal", filepath.Join(t.TempDir(), "repo"))
	for _, args := range [][]string{nil, {"status"}, {"dashboard", "--json"}, {"--profile", "default"}, {"--profile=missing"}} {
		if code := Run(args, "test"); code == 0 || selectedProfile != "default" {
			t.Fatalf("script or explicit selection fell back: %q, exit %d", args, code)
		}
	}
}

func TestPluralProfileFlagExplainsCorrection(t *testing.T) {
	for _, args := range [][]string{{"--profiles", "personal"}, {"--profiles=personal"}} {
		_, _, err := selectProfile(args)
		var cli *cliError
		if !errors.As(err, &cli) || cli.code != 2 || !strings.Contains(cli.msg, "singular --profile NAME") {
			t.Fatalf("unhelpful plural error: %v", err)
		}
	}
}

func TestStartupPickerFitsFiltersAndSelects(t *testing.T) {
	profiles := map[string]Config{}
	for _, name := range []string{"personal", "phin", strings.Repeat("z", 48)} {
		profiles[name] = Config{Target: "rest:https://user:secret@example.test/" + strings.Repeat("long/", 30) + "\x1b\n", BackupScope: scopeDataOnly}
	}
	for _, width := range []int{40, 60, 110} {
		m := newProfilePicker(profiles)
		next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m = next.(profilePicker)
		view := m.View().Content
		if lipgloss.Width(view) > width || lipgloss.Height(view) > 24 {
			t.Errorf("picker exceeds %dx24: %dx%d", width, lipgloss.Width(view), lipgloss.Height(view))
		}
		if strings.Contains(ansi.Strip(view), "secret") {
			t.Fatal("picker exposed credentials")
		}
		entry := m.list.Items()[0].(startupProfile)
		if entry.name != "personal" || strings.ContainsAny(entry.target, "\x1b\n") || strings.Contains(entry.FilterValue(), "secret") {
			t.Fatal("inventory is unsorted or unsafe")
		}
		m.list.Select(1)
		next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if next.(profilePicker).chosen != "phin" || cmd == nil {
			t.Fatal("Enter failed to choose highlighted profile")
		}
		m.list.SetFilterState(list.Filtering)
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if next.(profilePicker).chosen != "" {
			t.Fatal("filter acceptance opened a profile")
		}
		m.list.ResetFilter()
		next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		if next.(profilePicker).chosen != "" || cmd == nil {
			t.Fatal("Escape did not cancel")
		}
	}
}
