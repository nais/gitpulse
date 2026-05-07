package main

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/nais/gitpulse/internal/tui"
)

var (
	configFile    string
	jsonOutput    bool
	webOutput     bool
	watchMode     bool
	plainMode     bool
	watchInterval int
)

func main() {
	root := &cobra.Command{
		Use:   "gitpulse",
		Short: "Multi-repo dashboard for GitHub orgs",
		Long:  "Monitor repo health and personal activity across nais and navikt GitHub orgs.",
		RunE:  run,
	}

	root.Flags().StringVarP(&configFile, "config", "c", "", "Path to config file (default: ./config.toml or ~/.config/gitpulse/config.toml)")
	root.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	root.Flags().BoolVar(&webOutput, "web", false, "Start local web dashboard")
	root.Flags().BoolVarP(&watchMode, "watch", "w", false, "Auto-refresh periodically (plain mode)")
	root.Flags().BoolVar(&plainMode, "plain", false, "Plain table output (no TUI)")
	root.Flags().IntVar(&watchInterval, "interval", 60, "Refresh interval in seconds")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig(configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	client, err := newGitHubClient()
	if err != nil {
		return fmt.Errorf("creating github client: %w", err)
	}

	// Non-TUI modes
	if jsonOutput || plainMode || webOutput {
		if webOutput {
			return runWeb(cfg, client)
		}
		if watchMode {
			return runWatch(cfg, client)
		}
		return runOnce(cfg, client)
	}

	// Default: TUI mode
	return runTUI(cfg, client)
}

func runTUI(cfg *Config, client *GitHubClient) error {
	fetchFn := func() (*tui.DashboardData, error) {
		data, err := fetchAll(cfg, client)
		if err != nil {
			return nil, err
		}
		// Convert main.DashboardData → tui.DashboardData via JSON round-trip
		return convertToTUIData(data), nil
	}

	interval := time.Duration(watchInterval) * time.Second
	model := tui.NewModel(fetchFn, interval, cfg.LocalDirs)

	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func convertToTUIData(d *DashboardData) *tui.DashboardData {
	td := &tui.DashboardData{
		FetchedAt: d.FetchedAt,
	}
	for _, r := range d.RepoStatuses {
		td.RepoStatuses = append(td.RepoStatuses, tui.RepoStatus{
			FullName:      r.FullName,
			LastCommitMsg: r.LastCommitMsg,
			LastCommitAt:  r.LastCommitAt,
			CheckStatus:   r.CheckStatus,
			OpenPRs:       r.OpenPRs,
		})
	}
	for _, pr := range d.MyPRs {
		td.MyPRs = append(td.MyPRs, tui.PullRequest{
			Repo:        pr.Repo,
			Number:      pr.Number,
			Title:       pr.Title,
			State:       pr.State,
			UpdatedAt:   pr.UpdatedAt,
			URL:         pr.URL,
			Draft:       pr.Draft,
			CheckStatus: pr.CheckStatus,
		})
	}
	for _, p := range d.MyPushes {
		td.MyPushes = append(td.MyPushes, tui.PushEvent{
			Repo:        p.Repo,
			Branch:      p.Branch,
			Message:     p.Message,
			PushedAt:    p.PushedAt,
			SHA:         p.SHA,
			CheckStatus: p.CheckStatus,
		})
	}
	for _, r := range d.LocalRepos {
		td.LocalRepos = append(td.LocalRepos, tui.LocalRepoInfo{
			Name:          r.Name,
			Branch:        r.Branch,
			Dirty:         r.Dirty,
			DirtyFiles:    r.DirtyFiles,
			Unpushed:      r.Unpushed,
			LastCommitMsg: r.LastCommitMsg,
		})
	}
	for _, s := range d.AgentSessions {
		td.AgentSessions = append(td.AgentSessions, tui.AgentSessionInfo{
			Tool: s.Tool,
			Name: s.Name,
			Repo: s.Repo,
		})
	}
	return td
}
