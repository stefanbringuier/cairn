package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func overviewProfiles(t *testing.T) string {
	t.Helper()
	root := profileFixture(t)
	for _, name := range []string{"default", "nas", "cloud"} {
		saveFixtureProfile(t, name, filepath.Join(root, name))
		p := profileContext(name)
		if err := os.WriteFile(p.passwordFile(), []byte(name+" secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(unitDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unitDir(), "cairn-nas-snapshot.timer"), []byte("keep the original timer"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeOverviewTools(t, root)
	return root
}

func writeOverviewTools(t *testing.T, root string) {
	t.Helper()
	bin := filepath.Join(root, "tools")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	systemctl := `#!/bin/sh
printf '%s\n' "$*" >> "$CAIRN_SYSTEMCTL_LOG"
case "$*" in
  *--property=Version*) if [ "$CAIRN_MANAGER_DOWN" = 1 ]; then exit 1; fi; echo 258 ;;
  *is-enabled*cairn-cloud-backup.timer*) echo disabled; exit 1 ;;
  *is-enabled*) echo enabled ;;
  *show*--value*) echo active ;;
  *show*) printf 'ActiveState=active\n\nActiveState=inactive\n' ;;
esac
`
	restic := `#!/bin/sh
if [ "$1" = version ]; then echo 'restic 0.19.1'; exit 0; fi
printf '%s|%s\n' "$RESTIC_REPOSITORY" "$*" >> "$CAIRN_RESTIC_LOG"
if [ -n "$CAIRN_OFFLINE_REPO" ] && [ "$CAIRN_OFFLINE_REPO" = "$RESTIC_REPOSITORY" ]; then
  echo 'backend reporting offline-secret' >&2
  exit 10
fi
case " $* " in *' snapshots '*) echo '[]' ;; *) exit 10 ;; esac
`
	for name, script := range map[string]string{"systemctl": systemctl, "restic": restic} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(bin, name), 0o700); err != nil { // #nosec G302 -- private executable test shims
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("CAIRN_SYSTEMCTL_LOG", filepath.Join(root, "systemctl.log"))
	t.Setenv("CAIRN_RESTIC_LOG", filepath.Join(root, "restic.log"))
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304 -- the test owns each temporary fixture path
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestLiveProfileOverviewReadOnlyAndFailureRecovery(t *testing.T) {
	root := overviewProfiles(t)
	cloud, err := loadProfile("cloud")
	if err != nil {
		t.Fatal(err)
	}
	cloud.Target = "rest:https://token:password@example.test/a/long/destination?api_key=query-secret#fragment-secret\x1b"
	if err := cloud.save(); err != nil {
		t.Fatal(err)
	}
	badDir := profileContext("broken").configDir()
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "config.json"), []byte(`{raw-secret`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(badDir, profileContext("alias").configDir()); err != nil {
		t.Fatal(err)
	}
	files := []string{filepath.Join(unitDir(), "cairn-nas-snapshot.timer")}
	for _, name := range []profileContext{"default", "nas", "cloud"} {
		files = append(files, filepath.Join(name.configDir(), "config.json"), name.passwordFile())
	}
	before := make([]string, 0, len(files))
	for _, file := range files {
		before = append(before, readTestFile(t, file))
	}
	inventory := readOverview(1, "nas")
	got := make([]string, 0, len(inventory.rows))
	for _, row := range inventory.rows {
		got = append(got, row.name)
		if row.name == "cloud" && (row.target != "rest:https://example.test/a/long/destination" ||
			row.snapshot.Enabled == nil || !*row.snapshot.Enabled ||
			row.backup.Enabled == nil || *row.backup.Enabled) {
			t.Fatalf("bad cloud row: %+v", row)
		}
	}
	if !slices.Equal(got, []string{"alias", "broken", "cloud", "default", "nas"}) || inventory.problem != "" ||
		inventory.rows[0].problem != "Unsafe profile directory" || inventory.rows[1].problem == "" {
		t.Fatalf("bad inventory: %+v", inventory)
	}
	commands := readTestFile(t, filepath.Join(root, "systemctl.log"))
	for line := range strings.SplitSeq(strings.TrimSpace(commands), "\n") {
		if !strings.HasPrefix(line, "--user show --property=Version --value") && !strings.HasPrefix(line, "--user is-enabled ") {
			t.Fatalf("unexpected scheduler action %q", line)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "restic.log")); !os.IsNotExist(err) {
		t.Fatalf("overview contacted a repo: %v", err)
	}
	t.Setenv("CAIRN_MANAGER_DOWN", "1")
	if row := readOverview(2, "nas").rows[2]; row.snapshot.Enabled != nil || row.backup.Enabled != nil {
		t.Fatalf("missing user manager must yield unknown: %+v", row)
	}
	for i, file := range files {
		if got := readTestFile(t, file); got != before[i] {
			t.Fatalf("overview changed %s", file)
		}
	}
	nas, err := loadProfile("nas")
	if err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(nas)
	m.scr, m.w, m.h = profilesScreen, 40, 24
	m.profiles = newProfilesView()
	m.profiles.install(inventory)
	m.profiles.input.SetValue("LONG/DEST")
	m.profiles.index = 0
	if row, ok := m.profiles.selected(); !ok || row.name != "cloud" {
		t.Fatalf("destination filter selected %+v", row)
	}
	m.profiles.input.SetValue("query-secret")
	if m.profiles.input.Value() != "" && len(m.profiles.filtered()) != 0 {
		t.Fatal("filter can identify a hidden token")
	}
	m.profiles.input.SetValue("cloud")
	if err := os.Remove(filepath.Join(cloud.profile.configDir(), "config.json")); err != nil {
		t.Fatal(err)
	}
	next, _, _ := m.profileKey("enter")
	after := next.(model)
	if after.c.profile != "nas" || after.scr != profilesScreen || !strings.Contains(after.flash, "configuration changed") {
		t.Fatalf("failed switch altered the prior dashboard: %v, %v, %s", after.c.profile, after.scr, after.flash)
	}
	if after := readOverview(3, "cloud"); !strings.Contains(after.problem, "Current profile cloud is missing") {
		t.Fatalf("refresh hid removal: %+v", after)
	}
	if next, _, _ := after.profileKey("esc"); next.(model).profiles.input.Value() != "" {
		t.Fatal("escape did not clear filter first")
	}
}

func TestLiveProfileOverviewSwitchBindsOperationsAndDiscardsLateMessages(t *testing.T) {
	root := overviewProfiles(t)
	nas, err := loadProfile("nas")
	if err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(nas)
	m.w, m.h = 60, 24
	oldStatus := m.loadStatus()
	oldSnaps := m.loadSnaps()
	m.scr, m.snap = dirList, Snap{ID: "old-snapshot"}
	m.setList("old", []list.Item{item{title: "notes", node: Node{Path: "/tmp/notes"}}})
	pending, oldPreview, handled := m.browseKey("r")
	if !handled || oldPreview == nil || !pending.(model).restorePending {
		t.Fatal("preview was not started")
	}
	blocked, _ := pending.Update(tea.KeyPressMsg{Code: 'p'})
	if blocked.(model).scr != dirList || !blocked.(model).restorePending {
		t.Fatal("p bypassed a pending restore preview")
	}
	canceled, _ := blocked.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = canceled.(model)
	switchTo := func(m model, name string) model {
		t.Helper()
		m.scr = dash
		next, cmd := m.dashKey("p")
		inventory := cmd().(overviewMsg)
		next, _ = next.Update(inventory)
		m = next.(model)
		m.profiles.input.SetValue(name)
		m.profiles.index = 0
		next, _, _ = m.profileKey("enter")
		m = next.(model)
		if m.c.profile.name() != name || m.scr != dash || m.loaded || m.target.Path != "" || m.form != nil || len(m.cache) != 0 {
			t.Fatalf("switch retained old state: %q %v", m.c.profile, m.scr)
		}
		return m
	}
	// Start A's real fixture I/O while A is still the active profile. Hold
	// the completed messages off the event loop until B finishes loading.
	ready := make(chan struct{}, 3)
	release := make(chan struct{})
	late := make(chan tea.Msg, 3)
	for _, work := range []tea.Cmd{oldStatus, oldSnaps, oldPreview} {
		go func() {
			msg := work()
			ready <- struct{}{}
			<-release
			late <- msg
		}()
	}
	for range 3 {
		<-ready
	}
	m = switchTo(m, "cloud")
	if m.gen != 1 || selectedProfile != "default" {
		t.Fatal("switch changed CLI globals or did not advance generation")
	}
	b := m.liveStatus()().(statusMsg)
	b.full, b.st.Target = true, "cloud completed first"
	next, _ := m.Update(b)
	m = next.(model)
	close(release)
	var staleA statusMsg
	for range 3 {
		msg := <-late
		if st, ok := msg.(statusMsg); ok {
			staleA = st
		}
		next, _ := m.Update(msg)
		m = next.(model)
		if m.st.Profile != "cloud" || m.st.Target != "cloud completed first" ||
			m.scr != dash || !m.loaded || m.loading || m.flash != "" || len(m.cache) != 0 {
			t.Fatal("late message changed the destination dashboard")
		}
	}
	m = switchTo(m, "nas")
	if m.gen != 2 {
		t.Fatal("returning to prior name reused its generation")
	}
	staleA.st.Target = "stale"
	next, _ = m.Update(staleA)
	m = next.(model)
	if m.loaded || !m.loading || m.st.Target != "" {
		t.Fatal("A-old status overwrote A-new")
	}
	// Invoke one old closure again after returning to A and switching to B.
	// It still uses its original credentials even though the CLI global is default.
	m = switchTo(m, "cloud")
	_ = oldSnaps()
	restic := readTestFile(t, filepath.Join(root, "restic.log"))
	if strings.Count(restic, filepath.Join(root, "nas")+"|") != 4 ||
		strings.Count(restic, profileContext("nas").passwordFile()) != 4 {
		t.Fatalf("old operations lost their profile: %q", restic)
	}
	if got := nas.profile.undoCommand("/tmp/notes", "123"); !strings.Contains(got, "--profile nas") {
		t.Fatalf("undo lost original profile: %s", got)
	}
	m = switchTo(m, "nas")
	for _, phase := range []struct {
		screen screen
		run    bool
	}{{confirmRestore, false}, {dirList, true}} {
		m.scr, m.restoring = phase.screen, phase.run
		next, _ := m.Update(tea.KeyPressMsg{Code: 'p'})
		if got := next.(model); got.scr != phase.screen || got.c.profile != "nas" {
			t.Fatal("switch bypassed restore confirmation or running restore")
		}
	}
}

func TestProfilesRemainAvailableFromAnOfflineDashboard(t *testing.T) {
	root := overviewProfiles(t)
	nas, err := loadProfile("nas")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAIRN_OFFLINE_REPO", filepath.Join(root, "nas"))
	m := newTUIModel(nas)
	m.w, m.h = 110, 32
	status := m.loadStatus()
	current, _ := m.Update(status())
	m = current.(model)
	visible := m.View().Content
	if !strings.Contains(visible, "OFFLINE") || strings.Contains(visible, "offline-secret") {
		t.Fatalf("dashboard lost offline state or exposed error text: %s", visible)
	}
	open, inventory := m.dashKey("p")
	open, _ = open.Update(inventory())
	m = open.(model)
	m.profiles.input.SetValue("cloud")
	m.profiles.index = 0
	switched, loaded := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if loaded == nil {
		t.Fatal("switch did not load the chosen profile")
	}
	switched, _ = switched.Update(loaded())
	if got := switched.(model); got.c.profile.name() != "cloud" || !got.st.Online {
		t.Fatalf("offline repository prevented changing profiles: %+v", got.st)
	}
}

func TestProfileOverviewNarrowAndEmpty(t *testing.T) {
	profileFixture(t)
	m := newTUIModel(Config{profile: "default"})
	m.scr, m.w, m.h = profilesScreen, 40, 24
	m.profiles = newProfilesView()
	if s := m.View().Content; !strings.Contains(s, "No profiles") {
		t.Fatalf("empty inventory: %s", s)
	}
	m.profiles.rows = []overviewRow{{name: "cloud", target: strings.Repeat("long/", 80)}}
	m.profiles.input.SetValue("nothing")
	if s := m.View().Content; !strings.Contains(s, "No matching profiles") {
		t.Fatal("no-match state hidden")
	}
	m.profiles.input.SetValue("")
	view := m.View().Content
	if !strings.Contains(view, "cloud") || !strings.Contains(view, "open") ||
		lipgloss.Width(view) > 40 || lipgloss.Height(view) > 24 {
		t.Fatalf("narrow overview is not usable: %s", view)
	}
	if tuiError(errors.New("raw-token")) != "The operation failed. Check the selected profile from the terminal." {
		t.Fatal("TUI exposed backend error")
	}
}
