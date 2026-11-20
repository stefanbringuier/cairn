package app

import (
	"reflect"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

// Execute the inert component commands without a terminal, retaining the
// messages so each test controls precisely when they reach the model.
func componentMessages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	var children []tea.Cmd
	if batch, ok := msg.(tea.BatchMsg); ok {
		children = batch
	} else if reflect.TypeOf(msg) == teaSequenceType {
		children = reflect.ValueOf(msg).Convert(reflect.TypeFor[[]tea.Cmd]()).Interface().([]tea.Cmd)
	} else {
		return []tea.Msg{msg}
	}
	var messages []tea.Msg
	for _, child := range children {
		messages = append(messages, componentMessages(child)...)
	}
	return messages
}

func deliverComponentMessages(m model, messages []tea.Msg) model {
	for _, msg := range messages {
		next, _ := m.Update(msg)
		m = next.(model)
	}
	return m
}

func filterMockList(m model, title string) (model, []tea.Msg) {
	m.scr = snapList
	m.setList("inert entries", []list.Item{item{title: title}})
	m, _ = pressBrowseKey(m, '/')
	m, cmd := pressBrowseKey(m, 'x')
	return m, componentMessages(cmd)
}

func assertMockSelection(t *testing.T, m model, title string) {
	t.Helper()
	selected, _ := m.list.SelectedItem().(item)
	if selected.title != title || selected.snap.ID != "" || selected.node.Path != "" {
		t.Fatalf("expected inert row %q, got %+v", title, selected)
	}
}

func TestListFilterResultsRemainWithTheirSource(t *testing.T) {
	m, _ := newBrowseReadFixture(t)
	t.Run("profile switch", func(t *testing.T) {
		nas, old := filterMockList(m, "x NAS inert")
		nas, _ = pressBrowseKey(nas, tea.KeyEscape) // clear filter
		nas, _ = pressBrowseKey(nas, tea.KeyEscape) // leave list
		nas, inventory := pressBrowseKey(nas, 'p')
		nas = deliverComponentMessages(nas, componentMessages(inventory))
		nas.profiles.input.SetValue("cloud")
		nas.profiles.index = 0
		cloud, _ := pressBrowseKey(nas, tea.KeyEnter)
		if cloud.c.profile.name() != "cloud" || cloud.gen == nas.gen {
			t.Fatal("did not switch to the cloud profile")
		}
		cloud, now := filterMockList(cloud, "x cloud inert")
		cloud = deliverComponentMessages(cloud, now)
		assertMockSelection(t, cloud, "x cloud inert")
		assertMockSelection(t, deliverComponentMessages(cloud, old), "x cloud inert")
	})
	t.Run("same-profile replacement", func(t *testing.T) {
		oldList, old := filterMockList(m, "x archived inert")
		newList, now := filterMockList(oldList, "x current inert")
		newList = deliverComponentMessages(newList, now)
		assertMockSelection(t, newList, "x current inert")
		assertMockSelection(t, deliverComponentMessages(newList, old), "x current inert")
	})
	t.Run("same-list query replacement", func(t *testing.T) {
		m := m
		m.scr = snapList
		m.setList("inert entries", []list.Item{item{title: "xa prior"}, item{title: "xy current"}})
		m, _ = pressBrowseKey(m, '/')
		m, old := pressBrowseKey(m, 'x')
		m, now := pressBrowseKey(m, 'y')
		m = deliverComponentMessages(m, componentMessages(now))
		assertMockSelection(t, m, "xy current")
		assertMockSelection(t, deliverComponentMessages(m, componentMessages(old)), "xy current")
	})
}

func inertSettingsForm(m model) (model, *huh.Input, *string) {
	m, _ = pressBrowseKey(m, 'e')
	value := new(string)
	first := huh.NewInput().Title("Inert draft A").Value(value)
	second := huh.NewInput().Title("Inert draft B")
	m.form = huh.NewForm(huh.NewGroup(first), huh.NewGroup(second)).WithWidth(40).WithHeight(12)
	m = deliverComponentMessages(m, componentMessages(scopedComponentCmd(m.form.Init(), m.settingsScope())))
	return m, first, value
}

func TestSettingsCommandsRemainWithTheirForm(t *testing.T) {
	m, _ := newBrowseReadFixture(t)
	old, originalField, _ := inertSettingsForm(m)
	old, advance := pressBrowseKey(old, tea.KeyEnter)
	var pending []tea.Msg
	for _, msg := range componentMessages(advance) {
		next, cmd := old.Update(msg)
		old = next.(model)
		pending = append(pending, componentMessages(cmd)...)
	}
	if len(pending) == 0 {
		t.Fatal("inert form did not produce a deferred next-group event")
	}
	// Positive control: the actual generated event advances the form that
	// issued it. A second group exists so this cannot save any settings.
	check := deliverComponentMessages(old, pending)
	if check.scr != settings || check.form.State != huh.StateNormal || check.form.GetFocusedField() == originalField {
		t.Fatal("advancing an inert form unexpectedly submitted it")
	}

	away, _ := pressBrowseKey(old, tea.KeyEscape)
	replacement, first, draft := inertSettingsForm(away)
	if replacement.settingsInstance == old.settingsInstance {
		t.Fatal("reopening settings reused the form instance")
	}
	got := deliverComponentMessages(replacement, pending)
	if got.scr != settings || got.form.GetFocusedField() != first || got.form.State != huh.StateNormal {
		t.Fatal("an old form event advanced the replacement form")
	}
	got, _ = pressBrowseKey(got, 'z')
	if *draft != "z" || got.form.GetFocusedField() != first {
		t.Fatal("active form no longer receives ordinary input")
	}

	// Reuse an instance number in a new generation, as a profile switch does.
	cloud, second, _ := inertSettingsForm(newTUIModel(Config{profile: "cloud"}))
	cloud.gen = old.gen + 1
	if cloud.settingsInstance != old.settingsInstance {
		t.Fatal("cross-profile fixture did not collide on instance number")
	}
	got = deliverComponentMessages(cloud, pending)
	if got.scr != settings || got.form.GetFocusedField() != second || got.form.State != huh.StateNormal {
		t.Fatal("a prior profile's form event advanced this profile's form")
	}
}

func TestScopedComponentCommandsPreserveSequenceAndNestedBatch(t *testing.T) {
	scope := componentScope{kind: settingsComponent, generation: 2, instance: 3}
	first, last := func() tea.Msg { return "first" }, func() tea.Msg { return "last" }
	batch := tea.Batch(first, tea.RequestWindowSize)
	cmd := scopedComponentCmd(tea.Sequence(batch, last), scope)
	result := cmd()
	if reflect.TypeOf(result) != teaSequenceType {
		t.Fatalf("lost Bubble Tea sequence: %T", result)
	}
	children := reflect.ValueOf(result).Convert(reflect.TypeFor[[]tea.Cmd]()).Interface().([]tea.Cmd)
	if len(children) != 2 {
		t.Fatalf("expected two ordered commands, got %d", len(children))
	}
	if _, ok := children[0]().(tea.BatchMsg); !ok {
		t.Fatal("lost nested Bubble Tea batch")
	}
	messages := componentMessages(func() tea.Msg { return result })
	if len(messages) != 3 || messages[0] != (componentResult{scope, "first"}) ||
		reflect.TypeOf(messages[1]) != windowQueryType || messages[2] != (componentResult{scope, "last"}) {
		t.Fatalf("unexpected scoped leaves: %+v", messages)
	}
}
