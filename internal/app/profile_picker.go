package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Only interactive, unqualified dashboard startup calls this function. Choose
// before starting repository work, so no asynchronous job observes a changed
// profile. An explicit selection and scripted commands always use loadConfig.
func dashboardConfig(choose func(map[string]Config) (string, error)) (Config, error) {
	c, err := loadConfig()
	if !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	// A dangling default config symlink is a broken setup, not an absent one.
	if _, statErr := os.Lstat(filepath.Join(configRoot(), "config.json")); !os.IsNotExist(statErr) {
		return c, err
	}
	profiles, inventoryErr := configuredProfiles()
	if inventoryErr != nil {
		return Config{}, inventoryErr
	}
	if len(profiles) == 0 {
		return c, err
	}
	name, err := choose(profiles)
	if err != nil {
		return Config{}, err
	}
	// Re-read and validate after selection in case files changed while choosing.
	c, err = loadProfile(name)
	if err == nil {
		selectedProfile = name
	}
	return c, err
}

type startupProfile struct {
	name, scope, target string
}

func (p startupProfile) Title() string       { return p.name + " · " + p.scope }
func (p startupProfile) Description() string { return p.target }
func (p startupProfile) FilterValue() string { return p.name + " " + p.target }

type profilePicker struct {
	list   list.Model
	w, h   int
	chosen string
}

func newProfilePicker(profiles map[string]Config) profilePicker {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	items := make([]list.Item, 0, len(names))
	for _, name := range names {
		c := profiles[name]
		items = append(items, startupProfile{name, c.backupScope(), clean(redact(clean(c.Target)))})
	}
	m := profilePicker{list: newUIList()}
	m.list.Title = "Choose a profile"
	m.list.SetItems(items)
	return m
}

func chooseStartupProfile(profiles map[string]Config) (string, error) {
	final, err := tea.NewProgram(newProfilePicker(profiles)).Run()
	if err != nil {
		return "", err
	}
	if name := final.(profilePicker).chosen; name != "" {
		return name, nil
	}
	return "", errCancelled
}

func (m profilePicker) Init() tea.Cmd { return nil }

func (m profilePicker) keys() string {
	return uiKeys(uiWidth(m.w), "↑ ↓", "choose", "enter", "open", "/", "filter", "esc", "clear / quit", "ctrl+c", "quit")
}

func (m profilePicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.list.Styles.Title = accent.MaxWidth(max(uiWidth(m.w)-6, 1))
		m.list.SetSize(max(uiWidth(m.w)-4, 1), max(m.h-12-lipgloss.Height(m.keys()), 4))
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.list.FilterState() != list.Filtering {
			switch key {
			case "esc":
				if m.list.FilterState() == list.Unfiltered {
					return m, tea.Quit
				}
			case "q":
				return m, tea.Quit
			case "enter":
				if entry, ok := m.list.SelectedItem().(startupProfile); ok {
					m.chosen = entry.name
					return m, tea.Quit
				}
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m profilePicker) View() tea.View {
	width := uiWidth(m.w)
	parts := []string{
		uiHeader("profiles", "", width), "",
		trimUI(dim.Render("No default profile. Choose an existing profile."), width),
		panel.Padding(0, 1).Width(width).Render(m.list.View()),
		trimUI(dim.Render("Selection is for this session only."), width), "", m.keys(),
	}
	v := tea.NewView(lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(parts, "\n")))
	v.AltScreen, v.WindowTitle = true, "cairn · profiles"
	return v
}
