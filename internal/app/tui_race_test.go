package app

import (
	"path/filepath"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

func newBrowseReadFixture(t *testing.T) (model, string) {
	t.Helper()
	root := overviewProfiles(t)
	c, err := loadProfile("nas")
	if err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(c)
	m.w, m.h = 80, 24
	return m, filepath.Join(root, "browse")
}

func pressBrowseKey(m model, key rune) (model, tea.Cmd) {
	msg := tea.KeyPressMsg{Code: key}
	if key >= ' ' && key <= '~' {
		msg.Text = string(key)
	}
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

func TestBrowseReadDoesNotKeepSpinnerAfterNavigation(t *testing.T) {
	m, dir := newBrowseReadFixture(t)
	for _, tc := range []struct {
		name string
		key  rune
		want screen
	}{
		{"settings", 'e', settings},
		{"destination guide", 't', storageList},
		{"help", '?', helpScreen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pending, read := pressBrowseKey(m, 'r')
			away, _ := pressBrowseKey(pending, tc.key)
			got, _ := away.Update(read())
			state := got.(model)
			if state.scr != tc.want || state.busy != "" {
				t.Fatalf("late snapshot read changed screen or left spinner: screen=%v, busy=%q", state.scr, state.busy)
			}
		})
	}
	t.Run("Escape from directory", func(t *testing.T) {
		m := m
		m.scr, m.snap = dirList, Snap{ID: "browse-fixture"}
		pending, read := m.open(dir)
		away, _ := pressBrowseKey(pending, tea.KeyEscape)
		msg := read().(lsMsg)
		msg.err, msg.nodes = nil, []Node{{Name: "outdated", Path: filepath.Join(dir, "outdated"), Type: "file"}}
		got, _ := away.Update(msg)
		state := got.(model)
		if state.scr != dash || state.busy != "" || len(state.cache) != 0 {
			t.Fatalf("late directory read changed dashboard, cached nodes, or left spinner: screen=%v, busy=%q", state.scr, state.busy)
		}
	})
}

func TestBrowseReadIgnoresSupersededRequests(t *testing.T) {
	m, dir := newBrowseReadFixture(t)
	t.Run("two snapshot reads on same screen", func(t *testing.T) {
		older, first := pressBrowseKey(m, 'r')
		newer, second := pressBrowseKey(older, 'r')
		old := first().(snapsMsg)
		old.snaps = []Snap{{ID: "old"}}
		current := second().(snapsMsg)
		current.snaps = []Snap{{ID: "current"}}
		got, _ := newer.Update(old)
		state := got.(model)
		if state.scr != dash || state.busy != "loading snapshots" {
			t.Fatalf("old snapshot result took ownership of new read: screen=%v, busy=%q", state.scr, state.busy)
		}
		got, _ = state.Update(current)
		state = got.(model)
		if selected, _ := state.list.SelectedItem().(item); state.scr != snapList ||
			state.busy != "" || selected.snap.ID != "current" {
			t.Fatalf("new snapshot result did not finish its own read: screen=%v, busy=%q, selected=%q", state.scr, state.busy, selected.snap.ID)
		}
	})
	t.Run("two reads of same directory", func(t *testing.T) {
		m := m
		m.scr, m.snap = dirList, Snap{ID: "browse-fixture"}
		older, first := m.open(dir)
		newer, second := older.open(dir)
		old := first().(lsMsg)
		old.err, old.nodes = nil, []Node{{Name: "old", Path: filepath.Join(dir, "old"), Type: "file"}}
		current := second().(lsMsg)
		current.err, current.nodes = nil, []Node{{Name: "current", Path: filepath.Join(dir, "current"), Type: "file"}}
		got, _ := newer.Update(old)
		state := got.(model)
		if state.busy != "reading "+dir || len(state.cache) != 0 {
			t.Fatalf("old directory result took ownership of new read: busy=%q, cache=%d", state.busy, len(state.cache))
		}
		got, _ = state.Update(current)
		state = got.(model)
		if selected, _ := state.list.SelectedItem().(item); state.scr != dirList ||
			state.busy != "" || selected.node.Name != "current" {
			t.Fatalf("new directory result did not finish its own read: screen=%v, busy=%q, selected=%q", state.scr, state.busy, selected.node.Name)
		}
	})
	t.Run("directory read overtaken by restore preview", func(t *testing.T) {
		m := m
		m.cache = map[string][]Node{}
		m.scr, m.snap = dirList, Snap{ID: "browse-fixture"}
		m.setList(dir, []list.Item{item{title: "notes", node: Node{Path: filepath.Join(dir, "notes"), Type: "file"}}})
		pending, read := m.open(dir)
		if read == nil {
			t.Fatal("uncached directory read did not start")
		}
		previewing, preview := pressBrowseKey(pending, 'r')
		if preview == nil || !previewing.restorePending {
			t.Fatal("restore preview did not start")
		}
		got, _ := previewing.Update(read())
		state := got.(model)
		if !state.restorePending || state.busy != "checking what would change" || state.flash != "" {
			t.Fatalf("directory read changed the active restore preview: pending=%v, busy=%q, flash=%q", state.restorePending, state.busy, state.flash)
		}
	})
}
