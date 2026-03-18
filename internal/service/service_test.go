package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateBinary(t *testing.T) {
	tests := []struct {
		binary  string
		wantErr bool
	}{
		{"sekiad", false},
		{"sekia-github", false},
		{"sekia-slack", false},
		{"sekia-linear", false},
		{"sekia-google", false},
		{"sekia-mcp", true},
		{"unknown", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.binary, func(t *testing.T) {
			err := ValidateBinary(tt.binary)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateBinary(%q) error = %v, wantErr %v", tt.binary, err, tt.wantErr)
			}
		})
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"github-work", false},
		{"work", false},
		{"a", false},
		{"my-agent-123", false},
		{"GitHub-Work", false},
		{"", true},
		{"-starts-with-hyphen", true},
		{"ends-with-hyphen-", true},
		{"-", true},
		{"has space", true},
		{"has.dot", true},
		{"has/slash", true},
		{strings.Repeat("a", 64), false},
		{strings.Repeat("a", 65), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

func TestLogPath(t *testing.T) {
	path := LogPath("github-work")
	if !strings.HasSuffix(path, filepath.Join("logs", "github-work.log")) {
		t.Errorf("LogPath returned %q, expected suffix logs/github-work.log", path)
	}
}

func TestEnsureLogDir(t *testing.T) {
	// Save and restore HOME to use a temp dir.
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	err := EnsureLogDir()
	if err != nil {
		t.Fatalf("EnsureLogDir() error: %v", err)
	}

	logDir := LogDir()
	info, err := os.Stat(logDir)
	if err != nil {
		t.Fatalf("log dir does not exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("log dir is not a directory")
	}
}
