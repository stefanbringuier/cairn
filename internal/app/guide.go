package app

import (
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m model) showTargets() model {
	items := make([]list.Item, len(storageChoices))
	for i, s := range storageChoices {
		items[i] = s
	}
	m.scr, m.flash = storageList, ""
	m.setList("Where can I save backups?", items)
	return m
}

func (m model) showGuide(scr screen) model {
	if scr == helpScreen {
		m.returnTo = m.scr
	}
	m.scr, m.flash = scr, ""
	m.guide = viewport.New()
	m.resizeGuide()
	return m
}

func (m *model) resizeGuide() {
	width := uiWidth(m.w)
	m.guide.SetWidth(max(width-6, 1))
	m.guide.SetHeight(max(m.h-11-lipgloss.Height(uiKeys(width, uiKeyPairs(m.scr)...)), 3))
	text := basicHelp()
	if m.scr == storageDetails {
		s := m.destination
		text = accent.Render(s.Name) + "\n" + ink.Render(s.Summary) + "\n\n" +
			code.Render("SET UP FROM YOUR TERMINAL") + "\n" + s.Setup + "\n\n" +
			dim.Render(s.Detail) + "\n\n" +
			dim.Render("This is a setup guide. Your current destination stays in use until you run cairn init with another target.")
	}
	m.guide.SetContent(ansi.Wrap(text, m.guide.Width(), ""))
}

func basicHelp() string {
	return strings.Join([]string{
		accent.Render("A saved version. A way back."),
		"Snapshots save your home and any folders you add.",
		"Full backups also save system settings and package lists for a new machine.",
		"Both share one destination; shared data is stored only once.",
		"",
		code.Render("FROM THE DASHBOARD"),
		"s  Save a snapshot now.     b  Make a full backup now.",
		"r  Browse saved versions, open a folder, and select a file to restore.",
		"e  Change how often backups run and how many versions you keep.",
		"t  See setup guides for disks, NAS shares, and cloud storage.",
		"",
		code.Render("GET A FILE BACK"),
		"cairn restore notes.md         Preview, then ask before restoring.",
		"cairn restore notes.md -y      Skip the question for scripts and agents.",
		"cairn restore notes.md --at 3d Get the version from three days ago.",
		"Relative paths start in your current folder. Deleted files work too.",
		"An in-place restore saves your current version first, so you can undo it.",
		"",
		code.Render("UNDERSTAND THE DASHBOARD"),
		"LAST is the last saved version; NEXT is the next scheduled run.",
		"KEEP is retention: 24h,7d,4w keeps hourly, daily, and weekly versions.",
		"OFFLINE means the destination cannot be reached; OVERDUE means a run is late.",
		"",
		"cairn help <command> gives examples. cairn --skill is the agent reference.",
	}, "\n")
}
