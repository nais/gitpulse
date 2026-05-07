package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nais/gitpulse/internal/local"
)

type DashboardData struct {
	RepoStatuses  []RepoStatus      `json:"repo_statuses"`
	MyPRs         []PullRequest     `json:"my_prs"`
	MyPushes      []PushEvent       `json:"my_pushes"`
	LocalRepos    []LocalRepoStatus `json:"local_repos"`
	AgentSessions []AgentSessionInfo `json:"agent_sessions"`
	FetchedAt     time.Time         `json:"fetched_at"`
}

type LocalRepoStatus struct {
	Name          string `json:"name"`
	Branch        string `json:"branch"`
	Dirty         bool   `json:"dirty"`
	DirtyFiles    int    `json:"dirty_files"`
	Unpushed      int    `json:"unpushed"`
	LastCommitMsg string `json:"last_commit_msg"`
}

type AgentSessionInfo struct {
	Tool    string `json:"tool"`
	Name    string `json:"name"`
	Repo    string `json:"repo"` // org/name format
	RepoDir string `json:"repo_dir"`
}

func runOnce(cfg *Config, client *GitHubClient) error {
	data, err := fetchAll(cfg, client)
	if err != nil {
		return err
	}

	if jsonOutput {
		return displayJSON(data)
	}
	displayTable(data)
	return nil
}

func runWatch(cfg *Config, client *GitHubClient) error {
	for {
		// Clear screen
		fmt.Print("\033[H\033[2J")

		data, err := fetchAll(cfg, client)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		} else {
			if jsonOutput {
				displayJSON(data)
			} else {
				displayTable(data)
			}
		}

		fmt.Printf("\n⏱  Refreshing in %ds... (Ctrl+C to quit)\n", watchInterval)
		time.Sleep(time.Duration(watchInterval) * time.Second)
	}
}

func runWeb(cfg *Config, client *GitHubClient) error {
	fmt.Println("🌐 Web dashboard not yet implemented. Use table or JSON output for now.")
	return runOnce(cfg, client)
}

func fetchAll(cfg *Config, client *GitHubClient) (*DashboardData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	statuses, err := client.FetchRepoStatuses(ctx, cfg.Repos)
	if err != nil {
		return nil, fmt.Errorf("fetching repo statuses: %w", err)
	}

	prs, err := client.FetchMyPRs(ctx, cfg.Username, cfg.Orgs)
	if err != nil {
		return nil, fmt.Errorf("fetching PRs: %w", err)
	}

	pushes := fetchPushEvents(ctx, cfg.Username)
	enrichPushEvents(ctx, pushes)

	// Scan local repos for dirty/unpushed state
	var localRepos []LocalRepoStatus
	if len(cfg.LocalDirs) > 0 {
		scanned := local.ScanDirs(cfg.LocalDirs, cfg.Repos)
		for _, r := range scanned {
			localRepos = append(localRepos, LocalRepoStatus{
				Name:          r.Name,
				Branch:        r.Branch,
				Dirty:         r.Dirty,
				DirtyFiles:    r.DirtyFiles,
				Unpushed:      r.Unpushed,
				LastCommitMsg: r.LastCommitMsg,
			})
		}
	}

	// Detect active agent sessions (copilot, cplt)
	var agentSessions []AgentSessionInfo
	if len(cfg.LocalDirs) > 0 {
		detected := local.DetectSessions(cfg.LocalDirs)
		for _, s := range detected {
			// Derive org/name from the repo dir path
			repo := repoNameFromDir(s.RepoDir, cfg.LocalDirs)
			agentSessions = append(agentSessions, AgentSessionInfo{
				Tool:    s.Tool,
				Name:    s.Name,
				Repo:    repo,
				RepoDir: s.RepoDir,
			})
		}
	}

	return &DashboardData{
		RepoStatuses:  statuses,
		MyPRs:         prs,
		MyPushes:      pushes,
		LocalRepos:    localRepos,
		AgentSessions: agentSessions,
		FetchedAt:     time.Now(),
	}, nil
}

// repoNameFromDir derives "org/name" from a directory path and configured local dirs.
func repoNameFromDir(dir string, localDirs []string) string {
	for _, ld := range localDirs {
		expanded := expandLocalDir(ld)
		if strings.HasPrefix(dir, expanded+"/") {
			// dir = /Users/.../github.com/nais/pgrator, expanded = /Users/.../github.com/nais
			rest := strings.TrimPrefix(dir, expanded+"/")
			// rest = "pgrator" or "pgrator/subdir"
			parts := strings.SplitN(rest, "/", 2)
			org := filepath.Base(expanded) // "nais"
			return org + "/" + parts[0]
		}
	}
	// Fallback: last two path segments
	parts := strings.Split(dir, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return filepath.Base(dir)
}

func expandLocalDir(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func fetchPushEvents(ctx context.Context, username string) []PushEvent {
	// Extract push events: repo, branch, timestamp, full SHA
	jqExpr := `[.[] | select(.type == "PushEvent")] | .[:20] | .[] | [.repo.name, (.payload.ref | sub("^refs/heads/";"") ), .created_at, .payload.head] | @tsv`
	out, err := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("/users/%s/events?per_page=50", username),
		"--jq", jqExpr,
	).Output()
	if err != nil {
		return nil
	}

	var pushes []PushEvent
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}

		branch := parts[1]
		if branch == "main" || branch == "master" {
			continue
		}

		pushedAt, _ := time.Parse(time.RFC3339, parts[2])
		pushes = append(pushes, PushEvent{
			Repo:     parts[0],
			Branch:   branch,
			PushedAt: pushedAt,
			SHA:      parts[3], // full SHA
		})
	}

	// Limit to 10 to avoid too many API calls
	if len(pushes) > 10 {
		pushes = pushes[:10]
	}

	return pushes
}

// enrichPushEvents fetches commit message and CI status for each push event.
func enrichPushEvents(ctx context.Context, pushes []PushEvent) {
	for i, p := range pushes {
		// Fetch commit message
		out, err := exec.CommandContext(ctx, "gh", "api",
			fmt.Sprintf("repos/%s/commits/%s", p.Repo, p.SHA),
			"--jq", `(.commit.message | split("\n")[0])`,
		).Output()
		if err == nil {
			pushes[i].Message = strings.TrimSpace(string(out))
		}

		// Fetch CI status via check-runs API (commit status API is often empty)
		out, err = exec.CommandContext(ctx, "gh", "api",
			fmt.Sprintf("repos/%s/commits/%s/check-runs", p.Repo, p.SHA),
			"--jq", `[.check_runs[] | .conclusion] | if length == 0 then "" elif any(. == "failure") then "FAILURE" elif any(. == null) then "PENDING" elif all(. == "success" or . == "skipped" or . == "neutral") then "SUCCESS" else "PENDING" end`,
		).Output()
		if err == nil {
			state := strings.TrimSpace(string(out))
			if state != "" {
				pushes[i].CheckStatus = strings.ToLower(state)
			}
		}

		// Truncate SHA for display after using full SHA for API calls
		if len(pushes[i].SHA) > 7 {
			pushes[i].SHA = pushes[i].SHA[:7]
		}
	}
}

func displayJSON(data *DashboardData) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
