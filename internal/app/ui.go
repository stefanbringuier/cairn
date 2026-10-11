package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	ink   = lipgloss.NewStyle().Foreground(lipgloss.Color("#C0CAF5"))
	panel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#3B4261")).Padding(0, 2)
	keycap = lipgloss.NewStyle().Foreground(lipgloss.Color("#E0AF68")).
		Background(lipgloss.Color("#24283B")).Padding(0, 1).Bold(true)
)

func uiWidth(w int) int { return min(max(w-4, 1), 104) }

func trimUI(s string, width int) string { return ansi.Truncate(s, max(width, 1), "…") }

func uiRow(left, right string, width int) string {
	if lipgloss.Width(left)+lipgloss.Width(right)+2 > width {
		return trimUI(left+"  "+right, width)
	}
	return left + strings.Repeat(" ", width-lipgloss.Width(left)-lipgloss.Width(right)) + right
}

func uiHeader(section, host string, width int) string {
	brand := accent.Render(" ▰▰▰   cairn")
	if section != "" {
		brand += dim.Render("  /  " + section)
	}
	return trimUI(accent.Render("  ▰"), width) + "\n" +
		uiRow(brand, code.Render(clean(host)), width) + "\n" +
		trimUI(accent.Render("▰▰▰▰▰  ")+dim.Render("A way back to your files"), width)
}

func cliBrand() string { return uiHeader("", "", 56) + "\n" }

func uiKeys(width int, pairs ...string) string {
	var lines []string
	line := ""
	for i := 0; i+1 < len(pairs); i += 2 {
		entry := keycap.Render(pairs[i]) + " " + dim.Render(pairs[i+1])
		if line != "" && lipgloss.Width(line)+2+lipgloss.Width(entry) > width {
			lines, line = append(lines, line), ""
		}
		if line != "" {
			line += "  "
		}
		line += trimUI(entry, width)
	}
	return strings.Join(append(lines, line), "\n")
}

func uiKeyPairs(scr screen) []string {
	switch scr {
	case snapList, dirList:
		keys := []string{"enter", "open", "←", "back", "/", "filter", "esc", "dashboard"}
		if scr == dirList {
			keys = append([]string{"r", "restore"}, keys...)
		}
		return keys
	case confirmRestore:
		return []string{"y", "restore", "n", "cancel"}
	case settings:
		return []string{"← →", "frequency", "enter", "next / save", "esc", "cancel"}
	case storageList:
		return []string{"enter", "setup guide", "/", "filter", "esc", "dashboard", "?", "help"}
	case storageDetails, helpScreen:
		return []string{"↑ ↓", "scroll", "esc", "back"}
	default:
		return []string{"s", "snapshot", "b", "backup", "r", "restore", "e", "settings", "t", "targets", "?", "help", "q", "quit"}
	}
}

func (m *model) resizeList() {
	width := uiWidth(m.w)
	m.list.Styles.Title = accent.MaxWidth(max(width-6, 1))
	// Keep room for the header, border, wrapped keys and one status message.
	m.list.SetSize(max(width-4, 1), max(m.h-10-lipgloss.Height(uiKeys(width, uiKeyPairs(m.scr)...)), 4))
}

func (m model) settingsHeight() int {
	return min(max(m.h-11-lipgloss.Height(uiKeys(uiWidth(m.w), uiKeyPairs(settings)...)), 3), 12)
}

func uiDelegate(showDescription bool) list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.ShowDescription = showDescription
	d.Styles.NormalTitle = ink.PaddingLeft(2)
	d.Styles.NormalDesc = dim.PaddingLeft(2)
	d.Styles.SelectedTitle = accent.Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(lipgloss.Color("#E0AF68")).PaddingLeft(1)
	d.Styles.SelectedDesc = d.Styles.SelectedTitle.Bold(false).Foreground(lipgloss.Color("#A9B1CC"))
	d.Styles.DimmedTitle, d.Styles.DimmedDesc = dim.PaddingLeft(2), dim.PaddingLeft(2)
	d.Styles.FilterMatch = accent.Underline(true)
	return d
}

func newUIList() list.Model {
	l := list.New(nil, uiDelegate(true), 0, 0)
	l.SetShowHelp(false)
	l.DisableQuitKeybindings()
	l.Styles.Title = accent
	l.Styles.TitleBar = lipgloss.NewStyle().Padding(0, 0, 1, 2)
	l.Styles.StatusBar = dim.Padding(0, 0, 1, 2)
	l.Styles.NoItems = dim
	l.Styles.ActivePaginationDot = accent.SetString("●")
	l.Styles.InactivePaginationDot = dim.SetString("○")
	l.Styles.Filter.Focused.Prompt, l.Styles.Filter.Blurred.Prompt = accent, dim
	l.Styles.Filter.Focused.Text, l.Styles.Filter.Blurred.Text = ink, dim
	l.Styles.Filter.Cursor.Color = lipgloss.Color("#E0AF68")
	return l
}

func settingsTheme(isDark bool) *huh.Styles {
	t := huh.ThemeBase(isDark)
	t.FieldSeparator = lipgloss.NewStyle().SetString("\n")
	t.Focused.Base = t.Focused.Base.BorderForeground(lipgloss.Color("#E0AF68"))
	t.Focused.Title = accent
	t.Focused.Description = dim
	t.Focused.Option, t.Focused.SelectedOption = ink, good.Bold(true)
	t.Focused.SelectSelector, t.Focused.NextIndicator, t.Focused.PrevIndicator = accent.SetString("› "), code.SetString(" →"), code.SetString("← ")
	t.Focused.TextInput.Text, t.Focused.TextInput.Prompt = ink, accent.SetString("› ")
	t.Focused.TextInput.Cursor, t.Focused.TextInput.Placeholder = accent, dim
	t.Focused.ErrorIndicator, t.Focused.ErrorMessage = bad, bad
	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Title = code.Bold(true)
	t.Blurred.SelectSelector = dim.SetString("  ")
	t.Blurred.TextInput.Prompt = dim.SetString("  ")
	t.Help.ShortKey, t.Help.ShortDesc = code, dim
	return t
}

func jobBadge(j JobStatus, online bool) string {
	switch {
	case j.Running:
		return code.Render("● RUNNING")
	case !online:
		return bad.Render("○ OFFLINE")
	case j.Result == "failed":
		return bad.Render("✗ FAILED")
	case j.Result == "partial":
		return amber.Render("! PARTIAL")
	case j.Stale:
		return amber.Render("! OVERDUE")
	case j.Last != nil:
		return good.Render("✓ OK")
	default:
		return dim.Render("WAITING")
	}
}

func jobCard(kind string, j JobStatus, online bool, width int, compact bool) string {
	inside := width
	if !compact {
		inside = max(width-panel.GetHorizontalFrameSize(), 1)
	}
	title := accent.Render("SNAPSHOTS")
	if kind == "backup" {
		title = code.Bold(true).Render("FULL BACKUPS")
	}
	last := dim.Render("Waiting for the first run")
	if !online {
		last = dim.Render("History unavailable offline")
	} else if j.Last != nil {
		last = ink.Render(ago(*j.Last)) + dim.Render(" · "+j.LastID)
	}
	next := dim.Render("Waiting for the timer")
	if j.Next != nil {
		next = ink.Render(ago(*j.Next))
	} else if j.Every == "off" {
		next = dim.Render("Schedule paused")
	}
	row := func(label, value string) string {
		return trimUI(dim.Render(fmt.Sprintf("%-5s", label))+value, inside)
	}
	lines := []string{
		uiRow(title, jobBadge(j, online), inside),
		trimUI(ink.Render(fmt.Sprintf("%d kept", j.Count))+dim.Render("  ·  every "+j.Every), inside),
	}
	if !compact {
		lines = append(lines, "")
	}
	lines = append(lines,
		row("LAST", last),
		row("NEXT", next),
		row("KEEP", ink.Render(j.Keep)),
	)
	if j.Running {
		if !compact {
			lines = append(lines, "")
		}
		lines = append(lines, trimUI(bar(j.Percent, max(inside-6, 1))+code.Render(fmt.Sprintf(" %3.0f%%", j.Percent*100)), inside))
	}
	if compact {
		return strings.Join(lines, "\n")
	}
	return panel.Width(width).Render(strings.Join(lines, "\n"))
}

func renderDashboard(st Status, width, height int) string {
	body := dashboardLayout(st, width, false)
	if lipgloss.Height(body) > height {
		body = dashboardLayout(st, width, true)
	}
	return body
}

func dashboardLayout(st Status, width int, compact bool) string {
	connection := good.Render("● ONLINE")
	if !st.Online {
		connection = bad.Render("○ OFFLINE")
	}
	inside := max(width-panel.GetHorizontalFrameSize(), 1)
	if compact {
		inside = width
	}
	target := uiRow(dim.Render("BACKUP TARGET"), connection, inside) + "\n" + trimUI(ink.Render(clean(st.Target)), inside)
	if !st.Online && st.Error != "" {
		target += "\n" + trimUI(bad.Render(clean(st.Error)), inside)
	}
	var jobs string
	if width >= 76 {
		cardWidth := (width - 2) / 2
		jobs = lipgloss.JoinHorizontal(lipgloss.Top,
			jobCard("snapshot", st.Snapshot, st.Online, cardWidth, compact), "  ",
			jobCard("backup", st.Backup, st.Online, width-2-cardWidth, compact))
	} else {
		jobs = jobCard("snapshot", st.Snapshot, st.Online, width, compact) + "\n\n" + jobCard("backup", st.Backup, st.Online, width, compact)
	}
	if !compact {
		target = panel.Width(width).Render(target)
	}
	body := target + "\n\n" + jobs
	if !st.Scheduler {
		body += "\n" + trimUI(amber.Render("! ")+dim.Render("Timers unavailable · run snapshots and backups manually"), width)
	}
	return body
}

func (m model) restorePreview(width int) string {
	inside := max(width-panel.GetHorizontalFrameSize(), 1)
	lines := []string{
		accent.Render("Restore " + clean(filepath.Base(m.target.Path)) + "?"),
		trimUI(dim.Render(tildify(m.target.Path)), inside),
		trimUI(dim.Render("From ")+code.Render(m.snap.ShortID)+dim.Render(" · "+ago(m.snap.Time)), inside),
		"",
		uiRow(amber.Render(fmt.Sprintf("%d overwrite", m.pv.Updated)), good.Render(fmt.Sprintf("%d recreate", m.pv.Restored)), inside),
		dim.Render(fmt.Sprintf("%d files already unchanged", m.pv.Unchanged)),
		"",
		good.Render("✓ Your current version will be saved first"),
		dim.Render("  Newer files outside this snapshot are kept."),
	}
	for i, line := range lines {
		lines[i] = trimUI(line, inside)
	}
	if m.pv.Updated == 0 && m.pv.Restored == 0 {
		lines[5] = trimUI(good.Render("Already identical · nothing would change"), inside)
	}
	return panel.Padding(1, 2).Width(width).Render(strings.Join(lines, "\n"))
}
