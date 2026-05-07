package local

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

// AgentSession represents an active AI coding session in a repo.
type AgentSession struct {
	Tool    string // "copilot", "cplt"
	Name    string // session summary/name
	RepoDir string // working directory
}

// DetectSessions finds active AI agent sessions across local repos.
// Deduplicates: if cplt is running in a repo, the underlying copilot session is suppressed.
func DetectSessions(localDirs []string) []AgentSession {
	copilotSessions := detectCopilotSessions(localDirs)
	cpltSessions := detectCpltSessions(localDirs)

	// Build set of repos with cplt sessions
	cpltRepos := make(map[string]bool)
	for _, s := range cpltSessions {
		cpltRepos[s.RepoDir] = true
	}

	// Only include copilot sessions for repos without a cplt session
	var sessions []AgentSession
	for _, s := range copilotSessions {
		if !cpltRepos[s.RepoDir] {
			sessions = append(sessions, s)
		}
	}
	sessions = append(sessions, cpltSessions...)
	return sessions
}

// detectCopilotSessions scans ~/.copilot/session-state for active sessions.
func detectCopilotSessions(localDirs []string) []AgentSession {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	sessionDir := filepath.Join(home, ".copilot", "session-state")
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return nil
	}

	// Expand local dirs for matching
	expandedDirs := make([]string, 0, len(localDirs))
	for _, d := range localDirs {
		expandedDirs = append(expandedDirs, expandHome(d))
	}

	var sessions []AgentSession
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(sessionDir, entry.Name())

		// Find lock files
		locks, _ := filepath.Glob(filepath.Join(dir, "inuse.*.lock"))
		if len(locks) == 0 {
			continue
		}

		// Read PID from lock file and check if alive
		pidBytes, err := os.ReadFile(locks[0])
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
		if err != nil {
			continue
		}
		if !isProcessAlive(pid) {
			continue
		}

		// Read workspace.yaml for cwd and summary
		wsData, err := os.ReadFile(filepath.Join(dir, "workspace.yaml"))
		if err != nil {
			continue
		}
		var ws struct {
			CWD     string `yaml:"cwd"`
			Summary string `yaml:"summary"`
			Name    string `yaml:"name"`
		}
		if err := yaml.Unmarshal(wsData, &ws); err != nil {
			continue
		}

		// Check if this session's cwd is under one of our local dirs
		if !isUnderDirs(ws.CWD, expandedDirs) {
			continue
		}

		name := ws.Summary
		if name == "" {
			name = ws.Name
		}
		sessions = append(sessions, AgentSession{
			Tool:    "copilot",
			Name:    name,
			RepoDir: ws.CWD,
		})
	}
	return sessions
}

// detectCpltSessions finds running cplt processes and their working directories.
func detectCpltSessions(localDirs []string) []AgentSession {
	// Find cplt PIDs
	out, err := exec.Command("pgrep", "-x", "cplt").Output()
	if err != nil {
		return nil
	}

	expandedDirs := make([]string, 0, len(localDirs))
	for _, d := range localDirs {
		expandedDirs = append(expandedDirs, expandHome(d))
	}

	// Track unique cwds to avoid duplicates (multiple cplt processes per repo)
	seen := make(map[string]bool)
	var sessions []AgentSession

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}

		// Get working directory via lsof
		cwd := getCWD(line)
		if cwd == "" || seen[cwd] {
			continue
		}

		if !isUnderDirs(cwd, expandedDirs) {
			continue
		}

		seen[cwd] = true
		sessions = append(sessions, AgentSession{
			Tool:    "cplt",
			Name:    "agent session",
			RepoDir: cwd,
		})
	}
	return sessions
}

// getCWD gets the working directory of a process using lsof.
func getCWD(pid string) string {
	out, err := exec.Command("lsof", "-p", pid, "-Fn").Output()
	if err != nil {
		return ""
	}
	// lsof -Fn outputs: first line "p<pid>", then "fcwd", then "n<path>"
	lines := strings.Split(string(out), "\n")
	for i, l := range lines {
		if l == "fcwd" && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "n") {
			return lines[i+1][1:]
		}
	}
	return ""
}

func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

func isUnderDirs(path string, dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(path, d) {
			return true
		}
	}
	return false
}
