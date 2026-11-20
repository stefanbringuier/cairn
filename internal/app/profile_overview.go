package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type overviewRow struct {
	name, target, scope, problem string
	snapshot, backup             profileTimer
}

type overviewMsg struct {
	request int
	active  string
	rows    []overviewRow
	problem string
}

type profilesView struct {
	rows                  []overviewRow
	input                 textinput.Model
	filtering, refreshing bool
	index                 int
	problem               string
}

func newProfilesView() profilesView {
	input := textinput.New()
	input.Prompt = "Filter: "
	input.CharLimit = 256
	return profilesView{input: input}
}

// Just the inventory display, not a rewrite of the target or the CLI JSON.
func profileDisplayTarget(target string) string {
	target = clean(target)
	if filepath.IsAbs(target) || strings.HasPrefix(target, "~/") || strings.HasPrefix(target, "./") {
		return target
	}
	prefix, value := "", target
	if scheme, rest, found := strings.Cut(target, ":"); found &&
		slices.Contains(resticSchemes, scheme+":") && strings.Contains(rest, "://") {
		prefix, value = scheme+":", rest
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || u.Host == "" {
			return "URL destination (details hidden)"
		}
		u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
		return prefix + clean(u.String())
	}
	// Non-URL backend strings can still carry query credentials or userinfo.
	value, _, _ = strings.Cut(value, "?")
	value, _, _ = strings.Cut(value, "#")
	if strings.Contains(value, "@") && strings.Contains(value, ":") {
		return "remote destination (details hidden)"
	}
	return value
}

func readOverview(request int, active string) overviewMsg {
	result := overviewMsg{request: request, active: active}
	names := []string{"default"}
	dir := filepath.Join(configRoot(), "profiles")
	if checkProfilePath(dir) != nil {
		result.problem = "Cannot read profiles: the profiles location is not a safe directory."
	} else if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if name := entry.Name(); name != "default" && validProfile(name) &&
				(entry.IsDir() || entry.Type()&os.ModeSymlink != 0) {
				names = append(names, name)
			}
		}
	} else if !os.IsNotExist(err) {
		result.problem = "Cannot read the named-profile inventory."
	}
	slices.Sort(names)
	foundActive := false
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// In an environment without a user manager, is-enabled can still report
	// file symlinks. Do not mistake that for live scheduler information.
	managerChecked, managerAvailable := false, false
	for _, name := range names {
		c, err := loadProfile(name)
		if os.IsNotExist(err) && name == "default" && name != active {
			if _, statErr := os.Lstat(filepath.Join(configRoot(), "config.json")); os.IsNotExist(statErr) {
				continue
			}
		}
		row := overviewRow{name: name}
		if err != nil {
			row.problem = "Invalid or unreadable configuration"
			if os.IsNotExist(err) {
				row.problem = "Missing configuration"
			} else if checkProfileDirectory(name) != nil {
				row.problem = "Unsafe profile directory"
			}
		} else {
			foundActive = foundActive || name == active
			row.target, row.scope = profileDisplayTarget(c.Target), c.backupScope()
			p := c.operationProfile()
			row.snapshot = profileTimer{Every: c.Snapshot.Every, Timer: p.unitName("snapshot") + ".timer"}
			row.backup = profileTimer{Every: c.Backup.Every, Timer: p.unitName("backup") + ".timer"}
			if !managerChecked {
				out, err := systemctlWithContext(ctx, "show", "--property=Version", "--value")
				managerAvailable = err == nil && strings.TrimSpace(string(out)) != ""
				managerChecked = true
			}
			if managerAvailable {
				row.snapshot.Enabled = timerEnabledContext(ctx, row.snapshot.Timer)
				row.backup.Enabled = timerEnabledContext(ctx, row.backup.Timer)
			}
		}
		result.rows = append(result.rows, row)
	}
	if !foundActive {
		if result.problem != "" {
			result.problem += " "
		}
		result.problem += "Current profile " + clean(active) + " is missing or invalid."
	}
	return result
}

func (p profilesView) filtered() []overviewRow {
	filter := strings.ToLower(clean(p.input.Value()))
	return slices.DeleteFunc(slices.Clone(p.rows), func(row overviewRow) bool {
		return !strings.Contains(strings.ToLower(row.name), filter) &&
			!strings.Contains(strings.ToLower(row.target), filter)
	})
}

func (p profilesView) selected() (overviewRow, bool) {
	rows := p.filtered()
	if p.index < 0 || p.index >= len(rows) {
		return overviewRow{}, false
	}
	return rows[p.index], true
}

func (p *profilesView) install(msg overviewMsg) {
	old, had := p.selected()
	p.rows, p.problem, p.refreshing = msg.rows, msg.problem, false
	rows := p.filtered()
	p.index = min(p.index, max(0, len(rows)-1))
	wanted := msg.active
	if had {
		wanted = old.name
	}
	p.selectName(wanted, rows)
}

func (p *profilesView) selectName(name string, rows []overviewRow) {
	for i, row := range rows {
		if row.name == name {
			p.index = i
			return
		}
	}
}

func (p *profilesView) clearFilter(active string) {
	p.input.SetValue("")
	p.index = 0
	p.selectName(active, p.rows)
}

func (m model) viewingProfiles() bool {
	return m.scr == profilesScreen || (m.scr == helpScreen && m.returnTo == profilesScreen)
}

func (m *model) refreshProfiles() tea.Cmd {
	if m.profiles.refreshing {
		return nil
	}
	m.profileRequest++
	request, active := m.profileRequest, m.c.operationProfile().name()
	m.profiles.refreshing = true
	return func() tea.Msg { return readOverview(request, active) }
}

func (m model) profileKey(key string) (tea.Model, tea.Cmd, bool) {
	p := &m.profiles
	if p.filtering {
		switch key {
		case "enter", "esc":
			p.filtering, p.index = false, 0
			if key == "esc" {
				p.clearFilter(m.c.operationProfile().name())
			}
			p.input.Blur()
			return m, nil, true
		}
		return m, nil, false
	}
	switch key {
	case "/":
		p.filtering = true
		cmd := p.input.Focus()
		return m, cmd, true
	case "up", "k":
		p.index = max(0, p.index-1)
	case "down", "j":
		p.index = min(max(0, len(p.filtered())-1), p.index+1)
	case "esc":
		if p.input.Value() != "" {
			p.clearFilter(m.c.operationProfile().name())
		} else {
			m.scr, m.flash = dash, ""
		}
	case "r":
		cmd := m.refreshProfiles()
		return m, cmd, true
	case "enter":
		if row, ok := p.selected(); ok {
			if row.problem != "" {
				m.flash = bad.Render("✗ " + row.name + ": " + row.problem)
				return m, nil, true
			}
			c, err := loadProfile(row.name)
			if err != nil {
				m.flash = bad.Render("✗ Cannot open " + row.name + ": configuration changed.")
				cmd := m.refreshProfiles()
				return m, cmd, true
			}
			if m.restorePending || m.restoring {
				m.flash = bad.Render("✗ Finish or cancel the restore first.")
				return m, nil, true
			}
			// Do not probe repository availability as a condition of switching.
			// Reset profile-specific state by constructing a new dashboard.
			next := newTUIModel(c)
			next.w, next.h, next.gen = m.w, m.h, m.gen+1
			next.profileRequest, next.spin = m.profileRequest+1, m.spin
			cmd := next.loadStatus()
			return next, cmd, true
		}
	default:
		return m, nil, false
	}
	return m, nil, true
}

func formatProfileTimer(name string, timer profileTimer) string {
	state := "unknown"
	if timer.Enabled != nil {
		state = "disabled"
		if *timer.Enabled {
			state = "enabled"
		}
	}
	return fmt.Sprintf("%s: %s · %s", name, clean(timer.Every), state)
}

func (m model) profilesBody(width int) string {
	p := m.profiles
	inner := max(width-4, 1)
	p.input.SetWidth(max(inner-9, 1))
	lines := []string{accent.Render("Profiles"), "", dim.Render("Read-only · select a profile.")}
	if p.filtering {
		lines = append(lines, p.input.View())
	} else if p.input.Value() != "" {
		lines = append(lines, dim.Render("Filter: "+clean(p.input.Value())))
	}
	if p.problem != "" {
		lines = append(lines, bad.Render(p.problem))
	}
	if p.refreshing && len(p.rows) == 0 {
		lines = append(lines, dim.Render("Reading local profiles…"))
	}
	rows := p.filtered()
	if len(rows) == 0 && !p.refreshing {
		text := "No profiles are configured."
		if p.input.Value() != "" {
			text = "No matching profiles."
		}
		lines = append(lines, dim.Render(text))
	}
	available := m.h - 10 - len(lines) - lipgloss.Height(uiKeys(width, uiKeyPairs(profilesScreen)...))
	page := max(1, available/4)
	first := (p.index / page) * page
	if first > len(rows) {
		first = 0
	}
	for i := first; i < min(first+page, len(rows)); i++ {
		row := rows[i]
		prefix := "  "
		if i == p.index {
			prefix = "› "
		}
		label := row.name
		if row.name == m.c.operationProfile().name() {
			label += " · current"
		}
		if row.scope != "" {
			label += " · " + row.scope
		}
		lines = append(lines, accent.Render(prefix+label))
		if row.problem != "" {
			lines = append(lines, bad.Render("  "+row.problem), "", "")
			continue
		}
		lines = append(lines, dim.Render("  "+row.target),
			ink.Render("  "+formatProfileTimer("Snapshot", row.snapshot)),
			ink.Render("  "+formatProfileTimer("Backup", row.backup)))
	}
	if page < len(rows) {
		lines = append(lines, dim.Render(fmt.Sprintf("%d / %d profiles", p.index+1, len(rows))))
	}
	for i := range lines {
		lines[i] = trimUI(lines[i], inner)
	}
	return panel.Padding(0, 1).Width(width).Render(strings.Join(lines, "\n"))
}
