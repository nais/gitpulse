package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	// Create a temp config file
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	content := `username = "testuser"
orgs = ["nais", "navikt"]
repos = ["nais/deploy", "navikt/my-app"]
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	if cfg.Username != "testuser" {
		t.Errorf("expected username 'testuser', got %q", cfg.Username)
	}
	if len(cfg.Orgs) != 2 {
		t.Errorf("expected 2 orgs, got %d", len(cfg.Orgs))
	}
	if len(cfg.Repos) != 2 {
		t.Errorf("expected 2 repos, got %d", len(cfg.Repos))
	}
	if cfg.Repos[0] != "nais/deploy" {
		t.Errorf("expected first repo 'nais/deploy', got %q", cfg.Repos[0])
	}
}

func TestLoadConfigMissingUsername(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	content := `repos = ["nais/deploy"]
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	_, err := loadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error for missing username")
	}
}

func TestLoadConfigMissingRepos(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	content := `username = "testuser"
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	_, err := loadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected error for missing repos")
	}
}

func TestLoadConfigNotFound(t *testing.T) {
	_, err := loadConfig("/nonexistent/path.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"this is a long string", 10, "this is..."},
		{"multiline\nsecond line", 50, "multiline"},
		{"exact len!", 10, "exact len!"},
	}

	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.expected)
		}
	}
}

func TestTimeAgo(t *testing.T) {
	tests := []struct {
		input    time.Time
		expected string
	}{
		{time.Time{}, "-"},
		{time.Now().Add(-30 * time.Second), "just now"},
		{time.Now().Add(-5 * time.Minute), "5m ago"},
		{time.Now().Add(-3 * time.Hour), "3h ago"},
		{time.Now().Add(-48 * time.Hour), "2d ago"},
	}

	for _, tt := range tests {
		got := timeAgo(tt.input)
		if got != tt.expected {
			t.Errorf("timeAgo(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestCheckIcon(t *testing.T) {
	// Just verify no panics and non-empty output
	statuses := []string{"success", "failure", "error", "pending", "none", "unknown", "error: timeout"}
	for _, s := range statuses {
		got := checkIcon(s)
		if got == "" {
			t.Errorf("checkIcon(%q) returned empty string", s)
		}
	}
}

func TestMinInt(t *testing.T) {
	if minInt(3, 5) != 3 {
		t.Error("minInt(3,5) should be 3")
	}
	if minInt(7, 2) != 2 {
		t.Error("minInt(7,2) should be 2")
	}
}

func TestDisplayJSON(t *testing.T) {
	data := &DashboardData{
		RepoStatuses: []RepoStatus{
			{FullName: "nais/deploy", CheckStatus: "success", OpenPRs: 2},
		},
		MyPRs:     []PullRequest{},
		MyPushes:  []PushEvent{},
		FetchedAt: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	// Redirect stdout to verify no error
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := displayJSON(data)

	w.Close()
	os.Stdout = old
	r.Close()

	if err != nil {
		t.Fatalf("displayJSON failed: %v", err)
	}
}
