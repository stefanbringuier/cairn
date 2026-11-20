package app

import (
	"reflect"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

type componentKind uint8

const (
	listComponent componentKind = iota
	settingsComponent
)

type componentScope struct {
	kind                        componentKind
	generation, instance, query int
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
			return componentResult{scope, msg}
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
			if scope.kind == settingsComponent {
				return componentResult{scope, msg}
			}
			return msg
		}
	}
}

func (m model) listScope() componentScope {
	return componentScope{listComponent, m.gen, m.listInstance, m.filterRevision}
}

func (m model) settingsScope() componentScope {
	return componentScope{settingsComponent, m.gen, m.settingsInstance, 0}
}

func (m model) hasListScreen() bool {
	return m.scr == snapList || m.scr == dirList || m.scr == storageList
}

func (m *model) updateList(msg tea.Msg) tea.Cmd {
	text, state := m.list.FilterInput.Value(), m.list.FilterState()
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	if m.list.FilterInput.Value() != text || (state != list.Unfiltered && m.list.FilterState() == list.Unfiltered) {
		m.filterRevision++
	}
	return scopedComponentCmd(cmd, m.listScope())
}
