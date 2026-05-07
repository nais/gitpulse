package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"

	"github.com/nais/gitpulse/internal/cache"
)

// DashboardData mirrors the main package type for JSON round-tripping
type DashboardData struct {
	RepoStatuses  []RepoStatus       `json:"repo_statuses"`
	MyPRs         []PullRequest      `json:"my_prs"`
	MyPushes      []PushEvent        `json:"my_pushes"`
	LocalRepos    []LocalRepoInfo    `json:"local_repos"`
	AgentSessions []AgentSessionInfo `json:"agent_sessions"`
	FetchedAt     time.Time          `json:"fetched_at"`
}

type RepoStatus struct {
	FullName      string    `json:"full_name"`
	LastCommitMsg string    `json:"last_commit_msg"`
	LastCommitAt  time.Time `json:"last_commit_at"`
	CheckStatus   string    `json:"check_status"`
	OpenPRs       int       `json:"open_prs"`
}

type PullRequest struct {
	Repo        string    `json:"repo"`
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	State       string    `json:"state"`
	UpdatedAt   time.Time `json:"updated_at"`
	URL         string    `json:"url"`
	Draft       bool      `json:"draft"`
	CheckStatus string    `json:"check_status"`
}

type PushEvent struct {
	Repo        string    `json:"repo"`
	Branch      string    `json:"branch"`
	Message     string    `json:"message"`
	PushedAt    time.Time `json:"pushed_at"`
	SHA         string    `json:"sha"`
	CheckStatus string    `json:"check_status"`
}

type LocalRepoInfo struct {
	Name          string `json:"name"`
	Branch        string `json:"branch"`
	Dirty         bool   `json:"dirty"`
	DirtyFiles    int    `json:"dirty_files"`
	Unpushed      int    `json:"unpushed"`
	LastCommitMsg string `json:"last_commit_msg"`
}

type AgentSessionInfo struct {
	Tool string `json:"tool"`
	Name string `json:"name"`
	Repo string `json:"repo"`
}

// FetchFunc is called to fetch fresh data from GitHub.
type FetchFunc func() (*DashboardData, error)

// Messages
type dataFetchedMsg struct {
	data *DashboardData
	err  error
}

type cacheLoadedMsg struct {
	data  *DashboardData
	stale bool
	err   error
}

type refreshTickMsg time.Time
type clearAlertsMsg struct{}
type clearStatusMsg struct{}
type configUpdatedMsg struct{ err error }

// Model is the root TUI model
type Model struct {
	data        *DashboardData
	prevData    *DashboardData // previous fetch for diff detection
	fetchFn     FetchFunc
	interval    time.Duration
	cacheTTL    time.Duration
	spinner     spinner.Model
	loading     bool
	isStale     bool
	lastFetched time.Time
	err         error
	width       int
	height      int
	focusPanel  int    // 0=repos, 1=prs, 2=pushes, 3=local
	cursor      [4]int // selected row per panel
	visibleRows [4]int // visible rows per panel (updated during render)
	expanded    bool   // true = show only focused panel full-screen
	quitting    bool
	alerts      []string  // transient alerts shown on refresh
	alertExpiry time.Time // when to clear alerts
	localDirs   []string  // for opening local repos in editor
	configPath  string    // path to config.toml for live editing
	inputMode   bool      // true = text input overlay active
	textInput   textinput.Model
	statusMsg   string    // transient status message (e.g. "Repo added")
}

func NewModel(fetchFn FetchFunc, interval time.Duration, localDirs []string, configPath string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	ti := textinput.New()
	ti.Placeholder = "org/repo-name"
	ti.CharLimit = 100
	ti.Width = 40

	return Model{
		fetchFn:    fetchFn,
		interval:   interval,
		cacheTTL:   5 * time.Minute,
		spinner:    s,
		loading:    true,
		localDirs:  localDirs,
		configPath: configPath,
		textInput:  ti,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		loadCacheCmd(m.cacheTTL),
		fetchDataCmd(m.fetchFn),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Text input mode intercepts all keys
		if m.inputMode {
			switch msg.String() {
			case "enter":
				repo := strings.TrimSpace(m.textInput.Value())
				m.inputMode = false
				m.textInput.Reset()
				if repo != "" && strings.Contains(repo, "/") {
					return m, m.addRepoToConfig(repo)
				}
				return m, nil
			case "esc":
				m.inputMode = false
				m.textInput.Reset()
				return m, nil
			default:
				var cmd tea.Cmd
				m.textInput, cmd = m.textInput.Update(msg)
				return m, cmd
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "r":
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, fetchDataCmd(m.fetchFn))
		case "tab", "J":
			m.focusPanel = (m.focusPanel + 1) % 4
		case "shift+tab", "K":
			m.focusPanel = (m.focusPanel + 3) % 4
		case "j", "down":
			maxIdx := m.panelRowCount(m.focusPanel) - 1
			if !m.expanded && m.visibleRows[m.focusPanel] > 0 && m.visibleRows[m.focusPanel]-1 < maxIdx {
				maxIdx = m.visibleRows[m.focusPanel] - 1
			}
			if m.cursor[m.focusPanel] < maxIdx {
				m.cursor[m.focusPanel]++
			} else if !m.expanded {
				for i := 1; i <= 4; i++ {
					next := (m.focusPanel + i) % 4
					if m.panelRowCount(next) > 0 {
						m.focusPanel = next
						m.cursor[m.focusPanel] = 0
						break
					}
				}
			} else if m.cursor[m.focusPanel] < m.panelRowCount(m.focusPanel)-1 {
				m.cursor[m.focusPanel]++
			}
		case "k", "up":
			if m.cursor[m.focusPanel] > 0 {
				m.cursor[m.focusPanel]--
			} else if !m.expanded {
				for i := 1; i <= 4; i++ {
					prev := (m.focusPanel + 4 - i) % 4
					if m.panelRowCount(prev) > 0 {
						m.focusPanel = prev
						maxIdx := m.panelRowCount(prev) - 1
						if m.visibleRows[prev] > 0 && m.visibleRows[prev]-1 < maxIdx {
							maxIdx = m.visibleRows[prev] - 1
						}
						m.cursor[m.focusPanel] = maxIdx
						break
					}
				}
			}
		case "enter", "l":
			m.expanded = !m.expanded
		case "esc", "h":
			m.expanded = false
		case "o":
			return m, m.openInBrowser()
		case "e":
			return m, m.openInEditor()
		case "g":
			return m, m.openWithGH()
		case "a":
			// Add repo — open text input
			if m.configPath != "" {
				m.inputMode = true
				m.textInput.Focus()
				return m, textinput.Blink
			}
		case "d", "x":
			// Remove selected repo from config (only on repos panel)
			if m.focusPanel == 0 && m.configPath != "" && m.data != nil {
				idx := m.cursor[0]
				if idx < len(m.data.RepoStatuses) {
					repo := m.data.RepoStatuses[idx].FullName
					return m, m.removeRepoFromConfig(repo)
				}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateVisibleRows()

	case cacheLoadedMsg:
		if msg.err == nil && msg.data != nil {
			m.data = msg.data
			m.isStale = msg.stale
			m.lastFetched = msg.data.FetchedAt
			m.updateVisibleRows()
		}

	case dataFetchedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			// Detect new CI failures
			alerts := detectAlerts(m.data, msg.data)
			if len(alerts) > 0 {
				m.alerts = alerts
				m.alertExpiry = time.Now().Add(15 * time.Second)
			}
			m.prevData = m.data
			m.data = msg.data
			m.isStale = false
			m.lastFetched = time.Now()
			go func() {
				raw, _ := json.Marshal(msg.data)
				cache.Save(raw)
			}()
		}
		var cmds []tea.Cmd
		if m.interval > 0 {
			cmds = append(cmds, scheduleRefresh(m.interval))
		}
		if len(m.alerts) > 0 {
			cmds = append(cmds, tea.Tick(15*time.Second, func(t time.Time) tea.Msg {
				return clearAlertsMsg{}
			}))
		}
		return m, tea.Batch(cmds...)

	case configUpdatedMsg:
		if msg.err != nil {
			m.statusMsg = "Error: " + msg.err.Error()
		} else {
			m.statusMsg = "Config updated — press r to reload"
		}
		// Clear status after 5 seconds
		return m, tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
			return clearStatusMsg{}
		})

	case clearStatusMsg:
		m.statusMsg = ""

	case clearAlertsMsg:
		m.alerts = nil

	case refreshTickMsg:
		m.loading = true
		return m, tea.Batch(m.spinner.Tick, fetchDataCmd(m.fetchFn))

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	m.updateVisibleRows()
	return m, nil
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "Initializing..."
	}

	var b strings.Builder

	// Single-line header: title + spinner + tabs
	title := titleStyle.Render("gitpulse")
	if m.loading {
		title += " " + m.spinner.View()
	}
	tabs := m.renderTabs()
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(tabs)
	if gap < 1 {
		gap = 1
	}
	b.WriteString(title + strings.Repeat(" ", gap) + tabs)
	b.WriteString("\n")

	contentHeight := m.height - 3 // header + footer
	if contentHeight < 5 {
		contentHeight = 5
	}

	if m.inputMode {
		// Show input overlay
		b.WriteString("\n")
		b.WriteString(headingStyle.Render("  Add repo: "))
		b.WriteString(m.textInput.View())
		b.WriteString("\n")
		b.WriteString(dimStyleT.Render("  enter:confirm  esc:cancel"))
	} else if m.data == nil {
		if m.loading {
			b.WriteString(m.spinner.View() + " Loading data...")
		} else {
			b.WriteString("No data available. Press r to refresh.")
		}
	} else if m.expanded {
		b.WriteString(m.renderPanel(m.focusPanel, contentHeight))
	} else {
		b.WriteString(m.renderOverview(contentHeight))
	}

	b.WriteString("\n")
	b.WriteString(m.renderFooter())

	return b.String()
}

// Navigation and actions

func clampCursor(cursor *int, count int) {
	max := count - 1
	if max < 0 {
		max = 0
	}
	if *cursor < 0 {
		*cursor = 0
	}
	if *cursor > max {
		*cursor = max
	}
}

// scrollOffset returns the start index for a windowed view of items,
// keeping the cursor visible within maxVisible rows.
func scrollOffset(cursor, total, maxVisible int) int {
	if total <= maxVisible {
		return 0
	}
	if cursor < maxVisible {
		return 0
	}
	offset := cursor - maxVisible + 1
	if offset > total-maxVisible {
		offset = total - maxVisible
	}
	return offset
}

func (m *Model) updateVisibleRows() {
	if m.width == 0 || m.height == 0 || m.data == nil {
		return
	}
	contentHeight := m.height - 3
	if contentHeight < 5 {
		contentHeight = 5
	}

	if m.expanded {
		for i := 0; i < 4; i++ {
			m.visibleRows[i] = m.panelRowCount(i)
		}
		return
	}

	// Replicate overview height calculation (repos priority)
	panelOverhead := [4]int{4, 4, 4, 3}
	needed := [4]int{}
	for i := 0; i < 4; i++ {
		c := m.panelRowCount(i)
		if c == 0 {
			c = 1
		}
		needed[i] = c + panelOverhead[i]
	}

	heights := [4]int{}
	heights[0] = needed[0]
	remaining := contentHeight - heights[0]

	otherNeeded := needed[1] + needed[2] + needed[3]
	if otherNeeded <= remaining {
		heights[1] = needed[1]
		heights[2] = needed[2]
		heights[3] = needed[3]
	} else {
		for _, i := range []int{3, 2, 1} {
			h := needed[i]
			if h > remaining/2 && remaining > panelOverhead[i]+1 {
				h = remaining / 2
			}
			if h > remaining {
				h = remaining
			}
			if h < panelOverhead[i]+1 {
				h = panelOverhead[i] + 1
			}
			heights[i] = h
			remaining -= h
		}
		if remaining < 0 {
			heights[0] += remaining
			if heights[0] < panelOverhead[0]+1 {
				heights[0] = panelOverhead[0] + 1
			}
		}
	}

	for i := 0; i < 4; i++ {
		maxRows := heights[i] - panelOverhead[i]
		if maxRows < 1 {
			maxRows = 1
		}
		total := m.panelRowCount(i)
		if maxRows > total {
			maxRows = total
		}
		m.visibleRows[i] = maxRows
	}
}

func (m Model) panelRowCount(panel int) int {
	if m.data == nil {
		return 0
	}
	switch panel {
	case 0:
		return len(m.data.RepoStatuses)
	case 1:
		return len(m.data.MyPRs)
	case 2:
		return len(m.data.MyPushes)
	case 3:
		return len(m.data.LocalRepos)
	}
	return 0
}

func (m Model) selectedRepo() string {
	if m.data == nil {
		return ""
	}
	idx := m.cursor[m.focusPanel]
	switch m.focusPanel {
	case 0:
		if idx < len(m.data.RepoStatuses) {
			return m.data.RepoStatuses[idx].FullName
		}
	case 1:
		if idx < len(m.data.MyPRs) {
			return m.data.MyPRs[idx].Repo
		}
	case 2:
		if idx < len(m.data.MyPushes) {
			return m.data.MyPushes[idx].Repo
		}
	case 3:
		if idx < len(m.data.LocalRepos) {
			return m.data.LocalRepos[idx].Name
		}
	}
	return ""
}

func (m Model) openInBrowser() tea.Cmd {
	if m.data == nil {
		return nil
	}
	idx := m.cursor[m.focusPanel]
	switch m.focusPanel {
	case 1:
		// Open PR directly
		if idx < len(m.data.MyPRs) {
			pr := m.data.MyPRs[idx]
			return openURL(fmt.Sprintf("https://github.com/%s/pull/%d", pr.Repo, pr.Number))
		}
	case 2:
		// Open commit
		if idx < len(m.data.MyPushes) {
			p := m.data.MyPushes[idx]
			return openURL(fmt.Sprintf("https://github.com/%s/commit/%s", p.Repo, p.SHA))
		}
	}
	// Default: open repo page
	repo := m.selectedRepo()
	if repo != "" {
		return openURL("https://github.com/" + repo)
	}
	return nil
}

func (m Model) openInEditor() tea.Cmd {
	repo := m.selectedRepo()
	if repo == "" {
		return nil
	}
	path := m.findLocalPath(repo)
	if path == "" {
		return nil
	}
	return openEditor(path)
}

func (m Model) openWithGH() tea.Cmd {
	repo := m.selectedRepo()
	if repo == "" {
		return nil
	}
	return openGH(repo)
}

func (m Model) findLocalPath(repo string) string {
	// repo is "org/name", look in localDirs for matching path
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	for _, dir := range m.localDirs {
		expanded := expandHome(dir)
		candidate := expanded + "/" + parts[1]
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			return candidate
		}
		// Also try if the dir itself ends with the org
		if strings.HasSuffix(expanded, "/"+parts[0]) {
			candidate = expanded + "/" + parts[1]
			if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

func openURL(url string) tea.Cmd {
	return func() tea.Msg {
		exec.Command("open", url).Start()
		return nil
	}
}

func openEditor(path string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "code"
	}
	return func() tea.Msg {
		exec.Command(editor, path).Start()
		return nil
	}
}

func openGH(repo string) tea.Cmd {
	return func() tea.Msg {
		exec.Command("gh", "repo", "view", repo, "--web").Start()
		return nil
	}
}

// Config file manipulation

func (m Model) addRepoToConfig(repo string) tea.Cmd {
	return func() tea.Msg {
		err := appendRepoToConfig(m.configPath, repo)
		return configUpdatedMsg{err: err}
	}
}

func (m Model) removeRepoFromConfig(repo string) tea.Cmd {
	return func() tea.Msg {
		err := removeRepoFromConfig(m.configPath, repo)
		return configUpdatedMsg{err: err}
	}
}

// appendRepoToConfig adds a repo to the repos array in config.toml
func appendRepoToConfig(path, repo string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)

	// Check if already present
	if strings.Contains(content, `"`+repo+`"`) {
		return fmt.Errorf("%s already in config", repo)
	}

	// Find the closing bracket of the repos array and insert before it
	// Look for the last entry in repos = [...] and add after it
	lines := strings.Split(content, "\n")
	var result []string
	inRepos := false
	inserted := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "repos") && strings.Contains(trimmed, "[") {
			inRepos = true
		}
		if inRepos && !inserted && trimmed == "]" {
			// Insert new repo before closing bracket
			result = append(result, fmt.Sprintf(`  "%s",`, repo))
			inserted = true
		}
		if inRepos && trimmed == "]" {
			inRepos = false
		}
		result = append(result, line)
	}

	if !inserted {
		return fmt.Errorf("could not find repos array in config")
	}

	return os.WriteFile(path, []byte(strings.Join(result, "\n")), 0644)
}

// removeRepoFromConfig removes a repo from the repos array in config.toml
func removeRepoFromConfig(path, repo string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)

	lines := strings.Split(content, "\n")
	var result []string
	removed := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Match the repo entry (with or without trailing comma)
		if !removed && (trimmed == `"`+repo+`",` || trimmed == `"`+repo+`"`) {
			removed = true
			continue
		}
		result = append(result, line)
	}

	if !removed {
		return fmt.Errorf("%s not found in config", repo)
	}

	return os.WriteFile(path, []byte(strings.Join(result, "\n")), 0644)
}

func (m Model) renderTabs() string {
	names := []string{"Repos", "My PRs", "Pushes", "Local"}
	var parts []string
	for i, name := range names {
		if i == m.focusPanel {
			parts = append(parts, activeTabStyle.Render(name))
		} else {
			parts = append(parts, dimStyleT.Render(name))
		}
	}
	return strings.Join(parts, dimStyleT.Render(" │ "))
}

// Commands

func loadCacheCmd(ttl time.Duration) tea.Cmd {
	return func() tea.Msg {
		entry, err := cache.Load()
		if err != nil {
			return cacheLoadedMsg{err: err}
		}
		var data DashboardData
		if err := json.Unmarshal(entry.Data, &data); err != nil {
			return cacheLoadedMsg{err: err}
		}
		return cacheLoadedMsg{data: &data, stale: entry.IsStale(ttl)}
	}
}

func fetchDataCmd(fetchFn FetchFunc) tea.Cmd {
	return func() tea.Msg {
		data, err := fetchFn()
		return dataFetchedMsg{data: data, err: err}
	}
}

func scheduleRefresh(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return refreshTickMsg(t)
	})
}

// Rendering

var (
	activeTabStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99")).Underline(true)
	headingStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	staleStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyleT      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	successStyleT  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	failStyleT     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	pendingStyleT  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	selectedBg     = lipgloss.Color("24")  // visible highlight for selected row
	selectedFg     = lipgloss.Color("15")  // bright foreground for selected row
)

func (m Model) renderFooter() string {
	var parts []string

	// Alerts (visual bell) — shown prominently
	if len(m.alerts) > 0 {
		for _, a := range m.alerts {
			parts = append(parts, failStyleT.Render("▲ "+a))
		}
		return strings.Join(parts, "  ")
	}

	// Status message (config updates)
	if m.statusMsg != "" {
		parts = append(parts, successStyleT.Render("● "+m.statusMsg))
	}

	if m.loading {
		parts = append(parts, m.spinner.View()+" Fetching...")
	} else if m.isStale {
		parts = append(parts, staleStyle.Render("! Stale ("+shortDur(time.Since(m.lastFetched))+" ago)"))
	} else if !m.lastFetched.IsZero() {
		parts = append(parts, dimStyleT.Render("Updated "+shortDur(time.Since(m.lastFetched))+" ago"))
	}

	if m.err != nil {
		parts = append(parts, errorStyle.Render("! "+truncate(m.err.Error(), 40)))
	}

	if m.expanded {
		parts = append(parts, dimStyleT.Render("esc:back  j/k:select  J/K:panel  o:open  e:editor  g:gh  a:add  d:remove  r:refresh  q:quit"))
	} else {
		parts = append(parts, dimStyleT.Render("j/k:select  J/K:panel  enter:expand  o:open  e:editor  a:add  d:remove  r:refresh  q:quit"))
	}

	return strings.Join(parts, "  ")
}

func (m Model) renderOverview(totalHeight int) string {
	// Panel 0 (repos) gets priority — always show all rows
	// Remaining space distributed to panels 1-3, capping from bottom up
	panelOverhead := [4]int{4, 4, 4, 3} // heading + header + borders per panel

	needed := [4]int{}
	for i := 0; i < 4; i++ {
		c := m.panelRowCount(i)
		if c == 0 {
			c = 1
		}
		needed[i] = c + panelOverhead[i]
	}

	heights := [4]int{}
	// Give repos table its full height
	heights[0] = needed[0]
	remaining := totalHeight - heights[0]

	// Distribute remaining to panels 1, 2, 3
	otherNeeded := needed[1] + needed[2] + needed[3]
	if otherNeeded <= remaining {
		// Everything fits
		heights[1] = needed[1]
		heights[2] = needed[2]
		heights[3] = needed[3]
	} else {
		// Cap from bottom up: local gets minimum first, then pushes, then PRs
		for _, i := range []int{3, 2, 1} {
			h := needed[i]
			if h > remaining/2 && remaining > panelOverhead[i]+1 {
				h = remaining / 2
			}
			if h > remaining {
				h = remaining
			}
			if h < panelOverhead[i]+1 {
				h = panelOverhead[i] + 1
			}
			heights[i] = h
			remaining -= h
		}
		// If repos table needs to shrink to fit
		if remaining < 0 {
			heights[0] += remaining // reduce repos
			if heights[0] < panelOverhead[0]+1 {
				heights[0] = panelOverhead[0] + 1
			}
		}
	}

	var sections []string
	for i := 0; i < 4; i++ {
		sections = append(sections, m.renderPanel(i, heights[i]))
	}

	return strings.Join(sections, "\n")
}

func (m Model) panelHeading(panel int) string {
	names := []string{"Repos (default branch)", "My Open PRs", "Recent Pushes", "Local Repos"}
	if panel == m.focusPanel {
		return headingStyle.Render(names[panel])
	}
	return dimStyleT.Render(names[panel])
}

func (m Model) renderPanel(panel int, maxHeight int) string {
	heading := m.panelHeading(panel)
	switch panel {
	case 0:
		return heading + "\n" + m.renderReposTab(maxHeight-1)
	case 1:
		return heading + "\n" + m.renderPRsTab(maxHeight-1)
	case 2:
		return heading + "\n" + m.renderPushesTab(maxHeight-1)
	case 3:
		return heading + "\n" + m.renderLocalTab(maxHeight-1)
	}
	return ""
}

func (m Model) renderReposTab(maxHeight int) string {
	// Build local status lookup (git status + active agents)
	localStatus := make(map[string]string)
	for _, lr := range m.data.LocalRepos {
		if lr.Dirty && lr.Unpushed > 0 {
			localStatus[lr.Name] = fmt.Sprintf("~%d ↑%d", lr.DirtyFiles, lr.Unpushed)
		} else if lr.Dirty {
			localStatus[lr.Name] = fmt.Sprintf("~%d", lr.DirtyFiles)
		} else if lr.Unpushed > 0 {
			localStatus[lr.Name] = fmt.Sprintf("↑%d", lr.Unpushed)
		}
	}
	// Count agent sessions per repo
	agentCount := make(map[string][]string)
	for _, s := range m.data.AgentSessions {
		agentCount[s.Repo] = append(agentCount[s.Repo], s.Tool)
	}
	for repo, tools := range agentCount {
		indicator := ""
		for _, t := range tools {
			switch t {
			case "copilot":
				indicator += "◆"
			case "cplt":
				indicator += "●"
			}
		}
		if existing := localStatus[repo]; existing != "" {
			localStatus[repo] = indicator + " " + existing
		} else {
			localStatus[repo] = indicator
		}
	}

	// Compute REPO width from actual data (avoid wrapping)
	repoW := len("REPO")
	for _, r := range m.data.RepoStatuses {
		if len(r.FullName) > repoW {
			repoW = len(r.FullName)
		}
	}
	repoW += 2 // padding

	ageW := 11
	checksW := 5
	prsW := 5
	localW := 8
	borderW := 7
	commitW := m.width - repoW - ageW - checksW - prsW - localW - borderW
	if commitW < 20 {
		commitW = 20
	}

	cursor := m.cursor[0]
	maxRows := maxHeight - 4
	if maxRows < 1 {
		maxRows = 1
	}
	offset := scrollOffset(cursor, len(m.data.RepoStatuses), maxRows)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("238"))).
		Width(m.width).
		Headers("REPO", "LAST COMMIT", "AGE", "CI", "PRS", "LOCAL").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
			if row >= 0 && row+offset == cursor && m.focusPanel == 0 {
				s = s.Background(selectedBg).Foreground(selectedFg).Bold(true)
			}
			switch col {
			case 0:
				return s.Width(repoW)
			case 1:
				return s.Width(commitW)
			case 2:
				return s.Width(ageW)
			case 3:
				return s.Width(checksW).Align(lipgloss.Center)
			case 4:
				return s.Width(prsW).Align(lipgloss.Right)
			case 5:
				return s.Width(localW)
			}
			return s
		})

	end := offset + maxRows
	if end > len(m.data.RepoStatuses) {
		end = len(m.data.RepoStatuses)
	}
	for i := offset; i < end; i++ {
		r := m.data.RepoStatuses[i]
		local := localStatus[r.FullName]
		t.Row(
			r.FullName,
			truncate(r.LastCommitMsg, commitW-3),
			timeAgo(r.LastCommitAt),
			checkIcon(r.CheckStatus),
			prCount(r.OpenPRs),
			local,
		)
	}
	return t.String()
}

func (m Model) renderPRsTab(maxHeight int) string {
	if len(m.data.MyPRs) == 0 {
		return dimStyleT.Render("  No open PRs")
	}

	// Compute REPO width from data
	repoW := len("REPO")
	for _, pr := range m.data.MyPRs {
		if len(pr.Repo) > repoW {
			repoW = len(pr.Repo)
		}
	}
	repoW += 2

	numW := 7
	ciW := 5
	updW := 11
	borderW := 6
	titleW := m.width - repoW - numW - ciW - updW - borderW
	if titleW < 15 {
		titleW = 15
	}

	cursor := m.cursor[1]
	maxRows := maxHeight - 4
	if maxRows < 1 {
		maxRows = 1
	}
	offset := scrollOffset(cursor, len(m.data.MyPRs), maxRows)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("238"))).
		Width(m.width).
		Headers("REPO", "#", "TITLE", "CI", "UPDATED").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
			if row >= 0 && row+offset == cursor && m.focusPanel == 1 {
				s = s.Background(selectedBg).Foreground(selectedFg).Bold(true)
			}
			switch col {
			case 0:
				return s.Width(repoW)
			case 1:
				return s.Width(numW)
			case 2:
				return s.Width(titleW)
			case 3:
				return s.Width(ciW).Align(lipgloss.Center)
			case 4:
				return s.Width(updW)
			}
			return s
		})

	end := offset + maxRows
	if end > len(m.data.MyPRs) {
		end = len(m.data.MyPRs)
	}
	for i := offset; i < end; i++ {
		pr := m.data.MyPRs[i]
		title := pr.Title
		if pr.Draft {
			title = "[draft] " + title
		}
		t.Row(
			pr.Repo,
			fmt.Sprintf("#%d", pr.Number),
			truncate(title, titleW-3),
			checkIcon(pr.CheckStatus),
			timeAgo(pr.UpdatedAt),
		)
	}
	return t.String()
}

func (m Model) renderPushesTab(maxHeight int) string {
	if len(m.data.MyPushes) == 0 {
		return dimStyleT.Render("  No recent pushes")
	}

	// Compute widths from data
	repoW := len("REPO")
	branchW := len("BRANCH")
	for _, p := range m.data.MyPushes {
		if len(p.Repo) > repoW {
			repoW = len(p.Repo)
		}
		if len(p.Branch) > branchW {
			branchW = len(p.Branch)
		}
	}
	repoW += 2
	branchW += 2
	// Cap branch width to prevent one long branch from stealing all space
	if branchW > 25 {
		branchW = 25
	}

	ciW := 5
	whenW := 11
	shaW := 9
	borderW := 7
	msgW := m.width - repoW - branchW - ciW - whenW - shaW - borderW
	if msgW < 10 {
		msgW = 10
	}

	cursor := m.cursor[2]
	maxRows := maxHeight - 4
	if maxRows < 1 {
		maxRows = 1
	}
	offset := scrollOffset(cursor, len(m.data.MyPushes), maxRows)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("238"))).
		Width(m.width).
		Headers("REPO", "BRANCH", "MESSAGE", "CI", "WHEN", "SHA").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
			if row >= 0 && row+offset == cursor && m.focusPanel == 2 {
				s = s.Background(selectedBg).Foreground(selectedFg).Bold(true)
			}
			switch col {
			case 0:
				return s.Width(repoW)
			case 1:
				return s.Width(branchW)
			case 2:
				return s.Width(msgW)
			case 3:
				return s.Width(ciW)
			case 4:
				return s.Width(whenW)
			case 5:
				return s.Width(shaW)
			}
			return s
		})

	end := offset + maxRows
	if end > len(m.data.MyPushes) {
		end = len(m.data.MyPushes)
	}
	for i := offset; i < end; i++ {
		p := m.data.MyPushes[i]
		t.Row(
			p.Repo,
			truncate(p.Branch, branchW-3),
			truncate(p.Message, msgW-3),
			checkIcon(p.CheckStatus),
			timeAgo(p.PushedAt),
			p.SHA,
		)
	}
	return t.String()
}

func (m Model) renderLocalTab(maxHeight int) string {
	if m.data == nil || len(m.data.LocalRepos) == 0 {
		return dimStyleT.Render("  All clean")
	}

	repoW := len("REPO")
	branchW := len("BRANCH")
	for _, r := range m.data.LocalRepos {
		if len(r.Name) > repoW {
			repoW = len(r.Name)
		}
		if len(r.Branch) > branchW {
			branchW = len(r.Branch)
		}
	}
	repoW += 2
	branchW += 2
	if branchW > 25 {
		branchW = 25
	}

	statusW := 12
	borderW := 6
	commitW := m.width - repoW - branchW - statusW - borderW
	if commitW < 15 {
		commitW = 15
	}

	cursor := m.cursor[3]
	maxRows := maxHeight - 3
	if maxRows < 1 {
		maxRows = 1
	}
	offset := scrollOffset(cursor, len(m.data.LocalRepos), maxRows)
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("238"))).
		Width(m.width).
		Headers("REPO", "BRANCH", "STATUS", "LAST COMMIT").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
			if row >= 0 && row+offset == cursor && m.focusPanel == 3 {
				s = s.Background(selectedBg).Foreground(selectedFg).Bold(true)
			}
			switch col {
			case 0:
				return s.Width(repoW)
			case 1:
				return s.Width(branchW)
			case 2:
				return s.Width(statusW)
			case 3:
				return s.Width(commitW)
			}
			return s
		})

	end := offset + maxRows
	if end > len(m.data.LocalRepos) {
		end = len(m.data.LocalRepos)
	}
	for i := offset; i < end; i++ {
		r := m.data.LocalRepos[i]
		status := ""
		if r.Dirty && r.Unpushed > 0 {
			status = fmt.Sprintf("~%d ↑%d", r.DirtyFiles, r.Unpushed)
		} else if r.Dirty {
			status = fmt.Sprintf("~%d dirty", r.DirtyFiles)
		} else if r.Unpushed > 0 {
			status = fmt.Sprintf("↑%d unpushed", r.Unpushed)
		}
		t.Row(r.Name, truncate(r.Branch, branchW-3), status, truncate(r.LastCommitMsg, commitW-3))
	}

	return t.String()
}

// Helpers

func truncate(s string, maxLen int) string {
	s = strings.Split(s, "\n")[0]
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return dimStyleT.Render("-")
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "yesterday"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}

func shortDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

func checkIcon(status string) string {
	switch status {
	case "success":
		return successStyleT.Render("✓")
	case "failure", "error":
		return failStyleT.Render("✗")
	case "pending":
		return pendingStyleT.Render("●")
	case "none":
		return dimStyleT.Render("-")
	default:
		return dimStyleT.Render("?")
	}
}

func prCount(n int) string {
	if n == 0 {
		return dimStyleT.Render("0")
	}
	return pendingStyleT.Render(fmt.Sprintf("%d", n))
}

// detectAlerts compares previous and current data to find new problems
func detectAlerts(prev, curr *DashboardData) []string {
	if prev == nil || curr == nil {
		return nil
	}

	var alerts []string

	// Build map of previous CI statuses
	prevStatus := make(map[string]string)
	for _, r := range prev.RepoStatuses {
		prevStatus[r.FullName] = r.CheckStatus
	}

	// Detect new CI failures
	for _, r := range curr.RepoStatuses {
		old := prevStatus[r.FullName]
		if old != r.CheckStatus {
			switch r.CheckStatus {
			case "failure", "error":
				if old == "success" || old == "pending" || old == "" {
					alerts = append(alerts, r.FullName+" CI failed")
				}
			}
		}
	}

	// Detect new PRs opened
	prevPRs := make(map[string]bool)
	for _, pr := range prev.MyPRs {
		prevPRs[fmt.Sprintf("%s#%d", pr.Repo, pr.Number)] = true
	}
	for _, pr := range curr.MyPRs {
		key := fmt.Sprintf("%s#%d", pr.Repo, pr.Number)
		if !prevPRs[key] {
			alerts = append(alerts, "New PR: "+pr.Repo+"#"+fmt.Sprint(pr.Number))
		}
	}

	return alerts
}
