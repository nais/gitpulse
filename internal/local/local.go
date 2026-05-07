package local

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RepoStatus represents the local git status of a repository.
type RepoStatus struct {
	Path          string    // e.g. ~/go/src/github.com/nais/pgrator
	Name          string    // e.g. nais/pgrator
	Branch        string    // current branch
	Dirty         bool      // has uncommitted changes
	Unpushed      int       // commits ahead of remote
	DirtyFiles    int       // number of dirty files
	LastCommitMsg string    // latest commit message (first line)
	LastModified  time.Time // most recent commit or file change time
}

// ScanDirs scans the given directories for ALL git repos with dirty/unpushed state.
// Each dir is expected to contain repo directories (e.g. ~/go/src/github.com/nais/ contains pgrator/, unleash/, etc.)
// Results are sorted by most recently modified first.
func ScanDirs(dirs []string, repos []string) []RepoStatus {
	var results []RepoStatus
	for _, dir := range dirs {
		dir = expandHome(dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		org := filepath.Base(dir)

		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}

			repoPath := filepath.Join(dir, entry.Name())
			if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
				continue
			}

			repoName := org + "/" + entry.Name()
			status := checkRepo(repoPath, repoName)
			if status != nil {
				results = append(results, *status)
			}
		}
	}

	// Sort by most recently modified
	sort.Slice(results, func(i, j int) bool {
		return results[i].LastModified.After(results[j].LastModified)
	})

	return results
}

func checkRepo(path, name string) *RepoStatus {
	rs := &RepoStatus{
		Path: path,
		Name: name,
	}

	// Get current branch
	out, err := exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return nil
	}
	rs.Branch = strings.TrimSpace(string(out))

	// Check for dirty files
	out, err = exec.Command("git", "-C", path, "status", "--porcelain").Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if lines[0] != "" {
			rs.Dirty = true
			rs.DirtyFiles = len(lines)
		}
	}

	// Check unpushed commits (ahead of remote)
	out, err = exec.Command("git", "-C", path, "rev-list", "--count", "@{upstream}..HEAD").Output()
	if err == nil {
		count := strings.TrimSpace(string(out))
		if count != "0" {
			fmt.Sscanf(count, "%d", &rs.Unpushed)
		}
	}

	// Only include if there's something interesting
	if !rs.Dirty && rs.Unpushed == 0 {
		return nil
	}

	// Get latest commit message and time
	out, err = exec.Command("git", "-C", path, "log", "-1", "--format=%s").Output()
	if err == nil {
		rs.LastCommitMsg = strings.TrimSpace(string(out))
	}

	out, err = exec.Command("git", "-C", path, "log", "-1", "--format=%cI").Output()
	if err == nil {
		if t, e := time.Parse(time.RFC3339, strings.TrimSpace(string(out))); e == nil {
			rs.LastModified = t
		}
	}

	return rs
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
