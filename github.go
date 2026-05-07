package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

type GitHubClient struct {
	gql      *githubv4.Client
	username string
}

type RepoStatus struct {
	FullName      string
	LastCommitMsg string
	LastCommitAt  time.Time
	CheckStatus   string // success, failure, pending, unknown
	OpenPRs       int
}

type PullRequest struct {
	Repo      string
	Number      int
	Title       string
	State       string
	UpdatedAt   time.Time
	URL         string
	Draft       bool
	CheckStatus string
}

type PushEvent struct {
	Repo        string
	Branch      string
	Message     string
	PushedAt    time.Time
	SHA         string
	CheckStatus string
}

func newGitHubClient() (*GitHubClient, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		// Fallback to gh CLI
		out, err := exec.Command("gh", "auth", "token").Output()
		if err != nil {
			return nil, fmt.Errorf("GITHUB_TOKEN not set and `gh auth token` failed: %w", err)
		}
		token = strings.TrimSpace(string(out))
	}

	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	httpClient := oauth2.NewClient(context.Background(), src)
	gql := githubv4.NewClient(httpClient)

	return &GitHubClient{gql: gql}, nil
}

func (c *GitHubClient) FetchRepoStatuses(ctx context.Context, repos []string) ([]RepoStatus, error) {
	var results []RepoStatus

	for _, repo := range repos {
		parts := strings.SplitN(repo, "/", 2)
		if len(parts) != 2 {
			continue
		}
		owner, name := parts[0], parts[1]

		var query struct {
			Repository struct {
				DefaultBranchRef struct {
					Target struct {
						Commit struct {
							History struct {
								Nodes []struct {
									Message          string
									CommittedDate    time.Time
									StatusCheckRollup struct {
										State string
									}
								}
							} `graphql:"history(first: 1)"`
						} `graphql:"... on Commit"`
					}
				}
				PullRequests struct {
					TotalCount int
				} `graphql:"pullRequests(states: OPEN)"`
			} `graphql:"repository(owner: $owner, name: $name)"`
		}

		vars := map[string]interface{}{
			"owner": githubv4.String(owner),
			"name":  githubv4.String(name),
		}

		if err := c.gql.Query(ctx, &query, vars); err != nil {
			results = append(results, RepoStatus{
				FullName:    repo,
				CheckStatus: fmt.Sprintf("error: %v", err),
			})
			continue
		}

		status := RepoStatus{
			FullName: repo,
			OpenPRs:  query.Repository.PullRequests.TotalCount,
		}

		commits := query.Repository.DefaultBranchRef.Target.Commit.History.Nodes
		if len(commits) > 0 {
			status.LastCommitMsg = strings.Split(commits[0].Message, "\n")[0]
			status.LastCommitAt = commits[0].CommittedDate
			state := commits[0].StatusCheckRollup.State
			if state == "" {
				status.CheckStatus = "none"
			} else {
				status.CheckStatus = strings.ToLower(state)
			}
		}

		results = append(results, status)
	}

	return results, nil
}

func (c *GitHubClient) FetchMyPRs(ctx context.Context, username string, orgs []string) ([]PullRequest, error) {
	var allPRs []PullRequest

	for _, org := range orgs {
		var query struct {
			Search struct {
				Nodes []struct {
					PullRequest struct {
						Number    int
						Title     string
						State     string
						UpdatedAt time.Time
						URL       string
						IsDraft   bool
						Repository struct {
							NameWithOwner string
						}
						Commits struct {
							Nodes []struct {
								Commit struct {
									StatusCheckRollup struct {
										State string
									}
								}
							}
						} `graphql:"commits(last: 1)"`
					} `graphql:"... on PullRequest"`
				}
			} `graphql:"search(query: $query, type: ISSUE, first: 20)"`
		}

		searchQuery := fmt.Sprintf("is:pr author:%s org:%s is:open archived:false", username, org)
		vars := map[string]interface{}{
			"query": githubv4.String(searchQuery),
		}

		if err := c.gql.Query(ctx, &query, vars); err != nil {
			continue
		}

		for _, node := range query.Search.Nodes {
			pr := node.PullRequest
			checkStatus := ""
			if len(pr.Commits.Nodes) > 0 {
				checkStatus = strings.ToLower(pr.Commits.Nodes[0].Commit.StatusCheckRollup.State)
			}
			allPRs = append(allPRs, PullRequest{
				Repo:        pr.Repository.NameWithOwner,
				Number:      pr.Number,
				Title:       pr.Title,
				State:       pr.State,
				UpdatedAt:   pr.UpdatedAt,
				URL:         pr.URL,
				Draft:       pr.IsDraft,
				CheckStatus: checkStatus,
			})
		}
	}

	return allPRs, nil
}



func truncate(s string, maxLen int) string {
	s = strings.Split(s, "\n")[0]
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}
