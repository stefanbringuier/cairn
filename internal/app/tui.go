package app

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/dustin/go-humanize"
)

type screen int

const (
	dash screen = iota
	snapList
	dirList
	confirmRestore
	settings
	storageList
	profilesScreen
	storageDetails
	helpScreen
)

type item struct {
	title, desc string
	snap        Snap
	node        Node
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }

type (
	statusMsg struct {
		st   Status
		gen  int
		full bool // false: only the live systemd/progress fields were refreshed
	}
	tickMsg  time.Time
	snapsMsg struct {
		gen   int
		snaps []Snap
		err   error
	}
	lsMsg struct {
		gen         int
		snapID, dir string
		nodes       []Node
		err         error
	}
	previewMsg struct {
		gen, request int
		pv           preview
		node         Node
		err          error
	}
	doneMsg struct {
		gen        int
		text, undo string
		err        error
	}
)

type settingsValues struct{ snapEvery, snapKeep, backEvery, backKeep string }

type model struct {
	c                              Config
	st                             Status
	loaded                         bool
	loading                        bool // a full (repo) refresh is in flight; never stack them on slow remotes
	scr                            screen
	w, h                           int
	flash, undo                    string
	busy                           string
	spin                           spinner.Model
	list                           list.Model
	snap                           Snap
	dir                            string
	cache                          map[string][]Node
	target                         Node
	pv                             preview
	form                           *huh.Form
	fv                             *settingsValues
	guide                          viewport.Model
	destination                    storageChoice
	returnTo                       screen
	ticks                          int
	wasRunning                     bool
	gen                            int // incremented on switch and settings save
	profileRequest, previewRequest int
	profiles                       profilesView
	restorePending, restoring      bool
}

func newTUIModel(c Config) model {
	// A model is never allowed to defer resolving its profile until a job runs.
	c.profile = c.operationProfile()
	return model{
		c: c, st: Status{Profile: c.profile.name()}, cache: map[string][]Node{}, list: newUIList(),
		spin: spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(code)),
	}
}

func runTUI(c Config) error {
	m := newTUIModel(c)
	m.loading = true
	_, err := tea.NewProgram(m).Run()
	return err
}

// Keep the backend errors in non-TUI CLI status; a provider can put secrets in
// arbitrary error text. The dashboard needs a distinct outcome, not that text.
func dashboardStatus(c Config) Status {
	st := getStatus(c)
	st.Target = profileDisplayTarget(c.Target)
	st.Error = ""
	return st
}

func tuiError(err error) string {
	switch codeOf(err) {
	case exitNoRepo:
		return "Destination offline or no repository there."
	case exitBadPass:
		return "Wrong password for this repository."
	default:
		return "The operation failed. Check the selected profile from the terminal."
	}
}

func (m *model) loadStatus() tea.Cmd {
	if m.loading {
		return nil
	}
	m.loading = true
	c, gen := m.c, m.gen
	return func() tea.Msg { return statusMsg{dashboardStatus(c), gen, true} }
}

func (m model) liveStatus() tea.Cmd {
	st, gen := m.st, m.gen
	return func() tea.Msg { st.live(); return statusMsg{st, gen, false} }
}

func tick() tea.Cmd { return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }

func (m model) Init() tea.Cmd {
	c, gen := m.c, m.gen
	return tea.Batch(func() tea.Msg { return statusMsg{dashboardStatus(c), gen, true} }, tick(), m.spin.Tick)
}

func (m model) loadSnaps() tea.Cmd {
	c, gen := m.c, m.gen
	return func() tea.Msg {
		r, err := newRestic(c)
		if err != nil {
			return snapsMsg{gen: gen, err: err}
		}
		s, err := r.snapshots(c.Host)
		return snapsMsg{gen, s, err}
	}
}

func (m *model) setList(title string, items []list.Item) {
	m.list.SetDelegate(uiDelegate(m.scr != storageList))
	m.list.Title = title
	m.list.ResetFilter()
	m.list.SetItems(items)
	m.list.Select(0)
	m.resizeList()
}

func (m model) open(dir string) (model, tea.Cmd) {
	m.dir = dir
	if nodes, ok := m.cache[m.snap.ID+dir]; ok {
		return m.showDir(nodes), nil
	}
	m.busy = "reading " + tildify(dir)
	c, id, gen := m.c, m.snap.ID, m.gen
	return m, func() tea.Msg {
		r, err := newRestic(c)
		if err != nil {
			return lsMsg{gen: gen, snapID: id, dir: dir, err: err}
		}
		n, err := r.ls(id, dir)
		return lsMsg{gen, id, dir, n, err}
	}
}

func (m model) showDir(nodes []Node) model {
	var items []list.Item
	for _, n := range nodes {
		if n.Path == m.dir && n.Type == "dir" {
			continue // ls lists the dir itself first (a file source path shows as itself)
		}
		title, what := clean(n.Name), humanize.IBytes(uint64(max(n.Size, 0)))
		if n.Type == "dir" {
			title, what = title+"/", "folder"
		}
		items = append(items, item{title: title, desc: what + " · modified " + n.Mtime.Local().Format("2006-01-02 15:04"), node: n})
	}
	m.scr = dirList
	m.setList(m.snap.ShortID+" · "+tildify(m.dir), items)
	return m
}

// showRoots lists a multi-path snapshot's source paths.
func (m model) showRoots() model {
	var items []list.Item
	for _, p := range m.snap.Paths {
		items = append(items, item{title: tildify(p), desc: "source path", node: Node{Path: p, Type: "dir", Name: filepath.Base(p)}})
	}
	m.scr, m.dir = dirList, ""
	m.setList(m.snap.ShortID, items)
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.resizeList()
		if m.form != nil {
			m.form.WithWidth(max(uiWidth(m.w)-6, 1)).WithHeight(m.settingsHeight())
		}
		if m.scr == helpScreen || m.scr == storageDetails {
			m.resizeGuide()
		}
	case statusMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		if msg.full {
			m.loading = false
			m.st, m.loaded = msg.st, true
		} else if m.loaded {
			// A live poll owns only timer and progress fields; a delayed poll
			// must not restore an older full repository status.
			m.st.Scheduler = msg.st.Scheduler
			for _, kind := range []string{"snapshot", "backup"} {
				dst, src := m.st.job(kind), msg.st.job(kind)
				dst.Next, dst.Result, dst.Running, dst.Percent = src.Next, src.Result, src.Running, src.Percent
			}
		}
	case overviewMsg:
		if m.viewingProfiles() && msg.request == m.profileRequest {
			m.profiles.install(msg)
		}
		return m, nil
	case tickMsg:
		m.ticks++
		if m.viewingProfiles() {
			if m.ticks%3 == 0 {
				cmd := m.refreshProfiles()
				return m, tea.Batch(tick(), cmd)
			}
			return m, tick()
		}
		running := m.st.Snapshot.Running || m.st.Backup.Running
		cmds := []tea.Cmd{tick(), m.liveStatus()} // systemd + progress file: cheap, every second
		if m.loaded && ((m.wasRunning && !running) || m.ticks%30 == 0) {
			cmds = append(cmds, m.loadStatus()) // counts/last run: hits the repo, so only now and then
		}
		m.wasRunning = running
		return m, tea.Batch(cmds...)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case snapsMsg:
		if msg.gen != m.gen || m.busy != "loading snapshots" ||
			(m.scr != dash && m.scr != dirList && m.scr != snapList) {
			return m, nil
		}
		m.busy = ""
		if msg.err != nil {
			m.flash, m.scr = bad.Render("✗ "+tuiError(msg.err)), dash
			return m, nil
		}
		var items []list.Item
		for _, s := range slices.Backward(msg.snaps) {
			items = append(items, item{
				title: s.Time.Local().Format("2006-01-02 15:04") + "  " + s.kind(),
				desc:  s.ShortID + " · " + ago(s.Time) + " · " + tildifyAll(s.Paths), snap: s,
			})
		}
		m.scr = snapList
		m.setList("Saved versions", items)
	case lsMsg:
		if msg.gen != m.gen || msg.snapID != m.snap.ID || msg.dir != m.dir ||
			(m.scr != snapList && m.scr != dirList) {
			return m, nil
		}
		m.busy = ""
		if msg.err != nil {
			m.flash = bad.Render("✗ " + tuiError(msg.err))
			return m, nil
		}
		m.cache[msg.snapID+msg.dir] = msg.nodes
		m = m.showDir(msg.nodes)
	case previewMsg:
		if msg.gen != m.gen || !m.restorePending || msg.request != m.previewRequest {
			return m, nil
		}
		m.restorePending, m.busy = false, ""
		if msg.err != nil {
			m.flash = bad.Render("✗ " + tuiError(msg.err))
			return m, nil
		}
		m.pv, m.target, m.scr = msg.pv, msg.node, confirmRestore
	case doneMsg:
		if msg.gen != m.gen || !m.restoring {
			return m, nil
		}
		m.restoring, m.busy, m.undo = false, "", msg.undo
		m.resizeList()
		m.flash = good.Render("✓ " + msg.text)
		if msg.err != nil {
			m.flash = bad.Render("✗ " + tuiError(msg.err))
			if msg.text != "" {
				m.flash += " · " + clean(msg.text)
			}
		}
		cmd := m.loadStatus() // before returning m: it marks the refresh in flight
		return m, cmd
	case tea.KeyPressMsg:
		if m.restoring {
			return m, nil
		}
		if m.restorePending && msg.String() != "ctrl+c" {
			if msg.String() == "esc" || msg.String() == "q" {
				m.previewRequest++
				m.restorePending, m.busy = false, ""
			}
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if msg.String() == "?" && m.scr != settings && m.scr != helpScreen && m.scr != confirmRestore &&
			!m.profiles.filtering && m.list.FilterState() != list.Filtering {
			return m.showGuide(helpScreen), nil
		}
		switch m.scr {
		case dash:
			return m.dashKey(msg.String())
		case profilesScreen:
			if next, cmd, handled := m.profileKey(msg.String()); handled {
				return next, cmd
			}
		case snapList, dirList:
			if m.list.FilterState() != list.Filtering {
				if next, cmd, handled := m.browseKey(msg.String()); handled {
					return next, cmd
				}
			}
		case confirmRestore:
			switch msg.String() {
			case "y", "enter":
				m.busy, m.scr, m.restoring = "restoring "+tildify(m.target.Path), dirList, true
				c, s, n, p, gen := m.c, m.snap, m.target, m.target.Path, m.gen
				return m, func() tea.Msg {
					r, err := newRestic(c)
					if err != nil {
						return doneMsg{gen: gen, err: err}
					}
					undo, err := restoreInPlace(r, c, s, n, p)
					text := ""
					if err == nil {
						text = "restored " + tildify(p) + " from " + clean(s.ShortID)
					}
					if undo != "" {
						undo = "undo: " + c.operationProfile().undoCommand(p, undo)
					}
					return doneMsg{gen, text, undo, err}
				}
			case "n", "esc", "q":
				m.scr, m.pv, m.target = dirList, preview{}, Node{}
			}
			return m, nil
		case settings:
			if msg.String() == "esc" {
				m.scr = dash
				return m, nil
			}
		case storageList:
			if m.list.FilterState() != list.Filtering {
				switch msg.String() {
				case "esc", "q":
					if !m.list.IsFiltered() {
						m.scr = dash
						return m, nil
					}
				case "enter":
					if s, ok := m.list.SelectedItem().(storageChoice); ok {
						m.destination = s
						return m.showGuide(storageDetails), nil
					}
				}
			}
		case storageDetails, helpScreen:
			if msg.String() == "esc" || msg.String() == "q" || (msg.String() == "?" && m.scr == helpScreen) {
				if m.scr == storageDetails {
					m.scr = storageList
				} else {
					m.scr = m.returnTo
					if m.scr == storageDetails {
						m.resizeGuide()
					}
				}
				m.resizeList()
				return m, nil
			}
		}
	}
	if m.scr == profilesScreen && m.profiles.filtering {
		var cmd tea.Cmd
		m.profiles.input, cmd = m.profiles.input.Update(msg)
		if value := m.profiles.input.Value(); value != clean(value) {
			m.profiles.input.SetValue(clean(value))
		}
		m.profiles.index = 0
		return m, cmd
	}
	if m.scr == settings && m.form != nil {
		f, cmd := m.form.Update(msg)
		m.form = f.(*huh.Form)
		switch m.form.State {
		case huh.StateCompleted:
			m.scr, m.flash = dash, m.saveSettings()
			cmd := m.loadStatus()
			return m, cmd
		case huh.StateAborted:
			m.scr = dash
		}
		return m, cmd
	}
	if m.scr == helpScreen || m.scr == storageDetails {
		var cmd tea.Cmd
		m.guide, cmd = m.guide.Update(msg)
		return m, cmd
	}
	if m.scr == snapList || m.scr == dirList || m.scr == storageList {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) dashKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "q", "esc":
		return m, tea.Quit
	case "s", "b":
		kind := map[string]string{"s": "snapshot", "b": "backup"}[k]
		if err := m.c.operationProfile().startJob(kind); err != nil {
			m.flash = bad.Render("✗ Could not start " + kind + " on this profile.")
		} else {
			m.flash = dim.Render(kind + " started")
		}
		return m, m.liveStatus()
	case "r":
		m.busy, m.flash = "loading snapshots", ""
		return m, m.loadSnaps()
	case "p":
		if m.restorePending || m.restoring {
			return m, nil
		}
		m.scr, m.flash, m.busy = profilesScreen, "", ""
		m.profiles = newProfilesView()
		cmd := m.refreshProfiles()
		return m, cmd
	case "t":
		return m.showTargets(), nil
	case "e":
		m.flash = ""
		m.fv = &settingsValues{m.c.Snapshot.Every, m.c.Snapshot.Keep, m.c.Backup.Every, m.c.Backup.Keep}
		keepOK := func(s string) error { _, err := keepFlags(s); return err }
		m.form = huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Snapshot every").Inline(true).Options(huh.NewOptions(everyChoices...)...).Value(&m.fv.snapEvery),
			huh.NewInput().Title("Keep snapshots").Description("Hourly, daily, weekly · e.g. 24h,7d,4w").Value(&m.fv.snapKeep).Validate(keepOK),
			huh.NewSelect[string]().Title("Backup every").Inline(true).Options(huh.NewOptions(everyChoices...)...).Value(&m.fv.backEvery),
			huh.NewInput().Title("Keep backups").Description("Daily, weekly, monthly · e.g. 7d,4w,12m").Value(&m.fv.backKeep).Validate(keepOK),
		)).WithTheme(huh.ThemeFunc(settingsTheme)).WithShowHelp(false).
			WithWidth(max(uiWidth(m.w)-6, 1)).WithHeight(m.settingsHeight())
		m.scr = settings
		return m, m.form.Init()
	}
	return m, nil
}

// browseKey handles navigation keys; handled=false lets the list take the key (cursor, filter).
func (m model) browseKey(k string) (tea.Model, tea.Cmd, bool) {
	sel, _ := m.list.SelectedItem().(item)
	switch k {
	case "q", "esc":
		if m.list.IsFiltered() {
			return m, nil, false
		}
		m.scr = dash
		return m, nil, true
	case "enter", "right", "l":
		if m.scr == snapList && sel.snap.ID != "" {
			m.snap = sel.snap
			if len(m.snap.Paths) == 1 {
				next, cmd := m.open(m.snap.Paths[0])
				return next, cmd, true
			}
			return m.showRoots(), nil, true
		}
		if m.scr == dirList && sel.node.Type == "dir" {
			next, cmd := m.open(sel.node.Path)
			return next, cmd, true
		}
	case "backspace", "left", "h":
		if m.scr != dirList {
			m.scr = dash
			return m, nil, true
		}
		switch {
		case m.dir != "" && slices.Contains(m.snap.Paths, m.dir) && len(m.snap.Paths) > 1:
			return m.showRoots(), nil, true
		case m.dir == "" || slices.Contains(m.snap.Paths, m.dir):
			m.busy = "loading snapshots"
			return m, m.loadSnaps(), true
		}
		next, cmd := m.open(filepath.Dir(m.dir))
		return next, cmd, true
	case "r", "space", " ":
		if m.scr == dirList && sel.node.Path != "" {
			m.busy, m.restorePending, m.undo = "checking what would change", true, ""
			m.resizeList()
			m.previewRequest++
			c, s, n, gen, request := m.c, m.snap, sel.node, m.gen, m.previewRequest
			return m, func() tea.Msg {
				r, err := newRestic(c)
				if err != nil {
					return previewMsg{gen: gen, request: request, err: err}
				}
				pv, err := r.dryRun(s, n.Path)
				return previewMsg{gen, request, pv, n, err}
			}, true
		}
	}
	return m, nil, false
}

func (m *model) saveSettings() string {
	c := m.c
	for k, v := range map[string]string{"snapshot.every": m.fv.snapEvery, "snapshot.keep": m.fv.snapKeep, "backup.every": m.fv.backEvery, "backup.keep": m.fv.backKeep} {
		if err := c.set(k, v); err != nil {
			return bad.Render("✗ Check the frequency and retention values.")
		}
	}
	if err := c.save(); err != nil {
		return bad.Render("✗ Could not save settings for this profile.")
	}
	m.c, m.loading = c, false
	m.gen++
	if err := applySchedule(c); err != nil {
		return bad.Render("✗ Settings saved; timers could not be updated.")
	}
	return good.Render("✓ settings saved")
}

func (m model) View() tea.View {
	var body string
	width := uiWidth(m.w)
	section := "dashboard"
	keys := uiKeyPairs(m.scr)
	switch m.scr {
	case dash:
		body = dim.Render("loading…")
		if m.loaded {
			available := m.h - 7 - lipgloss.Height(uiKeys(width, keys...))
			if m.flash != "" || m.busy != "" {
				available--
			}
			body = renderDashboard(m.st, width, available)
		}
	case snapList, dirList:
		section = "restore"
		body = panel.Padding(0, 1).Width(width).Render(m.list.View())
	case confirmRestore:
		section, body = "restore / preview", m.restorePreview(width)
	case settings:
		section = "settings"
		body = panel.Padding(1, 2).Width(width).Render(m.form.View())
	case profilesScreen:
		section, body = "profiles", m.profilesBody(width)
	case storageList:
		section = "storage guide"
		body = panel.Padding(0, 1).Width(width).Render(m.list.View())
	case storageDetails, helpScreen:
		section = "help"
		if m.scr == storageDetails {
			section = "storage / setup"
		}
		body = panel.Padding(1, 2).Width(width).Render(m.guide.View())
	}
	status := m.flash
	if m.busy != "" {
		status = m.spin.View() + " " + dim.Render(m.busy)
	}
	profile := m.c.operationProfile().name()
	parts := []string{uiHeaderForProfile(section, m.c.Host, width, m.c.operationProfile()), "", body}
	if status != "" {
		parts = append(parts, trimUI(status, width))
	}
	if m.scr == dirList && m.undo != "" {
		parts = append(parts, m.undoNotice(width))
	}
	parts = append(parts, "", uiKeys(width, keys...))
	v := tea.NewView(lipgloss.NewStyle().Padding(1, 2).Render(strings.Join(parts, "\n")))
	v.AltScreen = true
	v.WindowTitle = "cairn · " + profile
	return v
}
