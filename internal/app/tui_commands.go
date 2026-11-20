package app

import (
	"reflect"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

type componentKind uint8

const (
	listComponent componentKind = iota
	listMatches
	pickerComponent
	settingsComponent
)

type componentScope struct {
	kind                                componentKind
	generation, instance, query, editor int
}

type componentResult struct {
	scope   componentScope
	message tea.Msg
}

// Bubble Tea exposes BatchMsg but keeps the []Cmd type returned by Sequence
// private. Derive its identity from the public constructor, then use the public
// constructor again to preserve ordering after scoping the child commands.
var (
	teaSequenceType = reflect.TypeOf(tea.Sequence(tea.RequestWindowSize, tea.RequestWindowSize)())
	windowQueryType = reflect.TypeOf(tea.RequestWindowSize())
)

func scopedComponentCmd(cmd tea.Cmd, scope componentScope) tea.Cmd {
	if cmd == nil {
		return nil
	}
	wrap := func(cmds []tea.Cmd) []tea.Cmd {
		wrapped := make([]tea.Cmd, len(cmds))
		for i, cmd := range cmds {
			wrapped[i] = scopedComponentCmd(cmd, scope)
		}
		return wrapped
	}
	return func() tea.Msg {
		switch msg := cmd().(type) {
		case nil:
			return nil
		case tea.BatchMsg:
			return tea.BatchMsg(wrap(msg))
		case list.FilterMatchesMsg:
			// A match for the current query can finish after Enter applies
			// it. Other replies, such as Ctrl+V, belong to the editor.
			match := scope
			if scope.kind == listComponent {
				match.kind, match.editor = listMatches, 0
			}
			return componentResult{match, msg}
		default:
			switch reflect.TypeOf(msg) {
			case teaSequenceType:
				value, sliceType := reflect.ValueOf(msg), reflect.TypeFor[[]tea.Cmd]()
				if !value.CanConvert(sliceType) {
					return nil // do not let unknown sequence internals escape the scope
				}
				children := value.Convert(sliceType).Interface().([]tea.Cmd)
				if sequence := tea.Sequence(wrap(children)...); sequence != nil {
					return sequence()
				}
				return nil
			case windowQueryType:
				return msg // requests a fresh global terminal size
			}
			return componentResult{scope, msg}
		}
	}
}

func (m model) listScope() componentScope {
	return componentScope{listComponent, m.gen, m.listInstance, m.filterRevision, m.listEditorRevision}
}

func (m model) ownsListEditor(scope componentScope) bool {
	current := m.listScope()
	// The query belongs to computed matches, not input arriving in the
	// same editor. In particular, two ordinary Ctrl+V requests can finish.
	scope.query, current.query = 0, 0
	return scope == current
}

func (m model) listMatchesScope() componentScope {
	scope := m.listScope()
	scope.kind, scope.editor = listMatches, 0
	return scope
}

func (m model) pickerScope() componentScope {
	return componentScope{kind: pickerComponent, generation: m.gen, instance: m.pickerEditorRevision}
}

func (m model) settingsScope() componentScope {
	return componentScope{kind: settingsComponent, generation: m.gen, instance: m.settingsInstance}
}

// Only actual terminal events can enter a component without an owner. In
// particular, Bubbles' private asynchronous paste result is not tea.PasteMsg.
func terminalComponentEvent(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg, tea.WindowSizeMsg, tea.BackgroundColorMsg, tea.MouseMsg:
		return true
	default:
		return false
	}
}

func (m model) hasListScreen() bool {
	return m.scr == snapList || m.scr == dirList || m.scr == storageList
}

func (m *model) updateList(msg tea.Msg) tea.Cmd {
	text, state := m.list.FilterInput.Value(), m.list.FilterState()
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	now := m.list.FilterState()
	if m.list.FilterInput.Value() != text || (state != list.Unfiltered && now == list.Unfiltered) {
		m.filterRevision++
	}
	if (state == list.Filtering) != (now == list.Filtering) {
		m.listEditorRevision++
	}
	return scopedComponentCmd(cmd, m.listScope())
}

func (m *model) updatePickerInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.profiles.input, cmd = m.profiles.input.Update(msg)
	if value := m.profiles.input.Value(); value != clean(value) {
		m.profiles.input.SetValue(clean(value))
	}
	m.profiles.index = 0
	return scopedComponentCmd(cmd, m.pickerScope())
}
