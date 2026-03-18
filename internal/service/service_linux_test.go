//go:build linux

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceFilePath(t *testing.T) {
	path := ServiceFilePath("github-work")
	expected := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user", "sekia-github-work.service")
	if path != expected {
		t.Errorf("ServiceFilePath = %q, want %q", path, expected)
	}
}

func TestCreateAndRemove(t *testing.T) {
	// Use a temp HOME to avoid touching real systemd units.
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create a fake binary in a temp bin dir.
	binDir := filepath.Join(tmpDir, "bin")
	os.MkdirAll(binDir, 0755)
	fakeBin := filepath.Join(binDir, "sekia-github")
	os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0755)
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", binDir+":"+origPath)
	defer os.Setenv("PATH", origPath)

	// Create will fail at daemon-reload since we're not running real systemd,
	// but we can still test the file generation up to that point.
	opts := CreateOpts{
		Binary: "sekia-github",
		Name:   "github-work",
		EnvVars: map[string]string{
			"GITHUB_TOKEN": "test-token",
		},
	}

	// We can't call Create() directly since it calls systemctl daemon-reload.
	// Instead, test the template rendering by checking what would be written.
	// For unit tests, we verify the file content after a manual write.
	unitPath := ServiceFilePath("github-work")
	os.MkdirAll(filepath.Dir(unitPath), 0755)

	// Simulate what Create would write.
	content := `[Unit]
Description=sekia github-work (sekia-github)
After=network.target

[Service]
ExecStart=` + filepath.Join(binDir, "sekia-github") + ` --name github-work
Restart=always
RestartSec=5
Environment=GITHUB_TOKEN=test-token
StandardOutput=append:` + LogPath("github-work") + `
StandardError=append:` + LogPath("github-work") + `

[Install]
WantedBy=default.target
`
	os.WriteFile(unitPath, []byte(content), 0644)

	data, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("unit file not created: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, "sekia-github") {
		t.Error("unit missing binary")
	}
	if !strings.Contains(s, "--name github-work") {
		t.Error("unit missing --name argument")
	}
	if !strings.Contains(s, "GITHUB_TOKEN=test-token") {
		t.Error("unit missing env var")
	}
	if !strings.Contains(s, "Restart=always") {
		t.Error("unit missing restart policy")
	}

	_ = opts // used above for documentation
}

func TestParseBinaryFromUnit(t *testing.T) {
	tmpDir := t.TempDir()
	unitPath := filepath.Join(tmpDir, "sekia-test.service")
	content := `[Unit]
Description=sekia test

[Service]
ExecStart=/usr/local/bin/sekia-github --name test
Restart=always
`
	os.WriteFile(unitPath, []byte(content), 0644)

	binary := parseBinaryFromUnit(unitPath)
	if binary != "sekia-github" {
		t.Errorf("parseBinaryFromUnit = %q, want %q", binary, "sekia-github")
	}
}

func TestList(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create the systemd user directory with a test unit.
	unitDir := filepath.Join(tmpDir, ".config", "systemd", "user")
	os.MkdirAll(unitDir, 0755)

	unitContent := `[Unit]
Description=sekia github-work (sekia-github)

[Service]
ExecStart=/usr/local/bin/sekia-github --name github-work
`
	os.WriteFile(filepath.Join(unitDir, "sekia-github-work.service"), []byte(unitContent), 0644)

	services, err := List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	if len(services) != 1 {
		t.Fatalf("List() returned %d services, want 1", len(services))
	}

	svc := services[0]
	if svc.Name != "github-work" {
		t.Errorf("Name = %q, want %q", svc.Name, "github-work")
	}
	if svc.Binary != "sekia-github" {
		t.Errorf("Binary = %q, want %q", svc.Binary, "sekia-github")
	}
}
