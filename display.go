package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	failureStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	pendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

func displayTable(data *DashboardData) {
	fmt.Println()
	fmt.Println(headerStyle.Render("📦 Repo Status"))
	fmt.Println()

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("238"))).
		Headers("REPO", "LAST COMMIT", "AGE", "CHECKS", "OPEN PRS")

	for _, r := range data.RepoStatuses {
		t.Row(
			r.FullName,
			truncate(r.LastCommitMsg, 40),
			timeAgo(r.LastCommitAt),
			checkIcon(r.CheckStatus),
			prCount(r.OpenPRs),
		)
	}

	fmt.Println(t)

	// My PRs
	if len(data.MyPRs) > 0 {
		fmt.Println()
		fmt.Println(headerStyle.Render("🔀 My Open PRs"))
		fmt.Println()

		pt := table.New().
			Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("238"))).
			Headers("REPO", "#", "TITLE", "UPDATED", "DRAFT")

		for _, pr := range data.MyPRs {
			draft := ""
			if pr.Draft {
				draft = "📝"
			}
			pt.Row(
				pr.Repo,
				fmt.Sprintf("#%d", pr.Number),
				truncate(pr.Title, 50),
				timeAgo(pr.UpdatedAt),
				draft,
			)
		}

		fmt.Println(pt)
	} else {
		fmt.Println()
		fmt.Println(dimStyle.Render("  No open PRs found."))
	}

	// My Pushes
	if len(data.MyPushes) > 0 {
		fmt.Println()
		fmt.Println(headerStyle.Render("⬆️  My Recent Pushes"))
		fmt.Println()

		pt := table.New().
			Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("238"))).
			Headers("REPO", "BRANCH", "MESSAGE", "WHEN", "SHA")

		for _, p := range data.MyPushes {
			pt.Row(
				p.Repo,
				p.Branch,
				p.Message,
				timeAgo(p.PushedAt),
				p.SHA,
			)
		}

		fmt.Println(pt)
	}

	fmt.Println()
	fmt.Println(dimStyle.Render(fmt.Sprintf("  Fetched at %s", data.FetchedAt.Format("15:04:05"))))
}

func checkIcon(status string) string {
	switch status {
	case "success":
		return successStyle.Render("✓ pass")
	case "failure", "error":
		return failureStyle.Render("✗ fail")
	case "pending":
		return pendingStyle.Render("● pending")
	case "none":
		return dimStyle.Render("- none")
	default:
		if strings.HasPrefix(status, "error:") {
			return failureStyle.Render("⚠ " + truncate(status, 20))
		}
		return dimStyle.Render("? " + status)
	}
}

func prCount(n int) string {
	if n == 0 {
		return dimStyle.Render("0")
	}
	return pendingStyle.Render(fmt.Sprintf("%d", n))
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return dimStyle.Render("-")
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
