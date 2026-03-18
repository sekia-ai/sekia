//go:build darwin

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceFilePath(t *testing.T) {
	path := ServiceFilePath("github-work")
	expected := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", "com.sekia.github-work.plist")
	if path != expected {
		t.Errorf("ServiceFilePath = %q, want %q", path, expected)
	}
}

func TestCreateAndRemove(t *testing.T) {
	// Use a temp HOME to avoid touching real LaunchAgents.
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create a fake binary in a temp bin dir.
	binDir := filepath.Join(tmpDir, "bin")
	os.MkdirAll(binDir, 0755)
	fakeBin := filepath.Join(binDir, "sekia-github")
	os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0755)
	os.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	// Create service.
	opts := CreateOpts{
		Binary: "sekia-github",
		Name:   "github-work",
		EnvVars: map[string]string{
			"GITHUB_TOKEN": "test-token",
		},
	}
	if err := Create(opts); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	plistPath := ServiceFilePath("github-work")
	data, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("plist not created: %v", err)
	}

	content := string(data)

	// Verify plist content.
	if !strings.Contains(content, "<string>com.sekia.github-work</string>") {
		t.Error("plist missing label")
	}
	if !strings.Contains(content, "<string>--name</string>") {
		t.Error("plist missing --name argument")
	}
	if !strings.Contains(content, "<string>github-work</string>") {
		t.Error("plist missing instance name")
	}
	if !strings.Contains(content, "sekia-github") {
		t.Error("plist missing binary path")
	}
	if !strings.Contains(content, "<key>GITHUB_TOKEN</key>") {
		t.Error("plist missing env var key")
	}
	if !strings.Contains(content, "<string>test-token</string>") {
		t.Error("plist missing env var value")
	}
	if !strings.Contains(content, "<key>KeepAlive</key>") {
		t.Error("plist missing KeepAlive")
	}

	// Create should fail if already exists.
	if err := Create(opts); err == nil {
		t.Error("expected error creating duplicate service")
	}

	// Remove should delete the plist.
	// Note: we skip the launchctl unload since it won't be loaded in tests.
	if err := os.Remove(plistPath); err != nil {
		t.Fatalf("failed to remove plist: %v", err)
	}
}

func TestCreateWithConfig(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	binDir := filepath.Join(tmpDir, "bin")
	os.MkdirAll(binDir, 0755)
	fakeBin := filepath.Join(binDir, "sekia-github")
	os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0755)
	os.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	opts := CreateOpts{
		Binary:     "sekia-github",
		Name:       "github-personal",
		ConfigPath: "/etc/sekia/github-personal.toml",
	}
	if err := Create(opts); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	data, err := os.ReadFile(ServiceFilePath("github-personal"))
	if err != nil {
		t.Fatalf("plist not created: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "<string>--config</string>") {
		t.Error("plist missing --config argument")
	}
	if !strings.Contains(content, "<string>/etc/sekia/github-personal.toml</string>") {
		t.Error("plist missing config path")
	}
}

func TestParseBinaryFromPlist(t *testing.T) {
	tmpDir := t.TempDir()
	plistPath := filepath.Join(tmpDir, "com.sekia.test.plist")
	content := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>ProgramArguments</key>
	<array>
		<string>/opt/homebrew/bin/sekia-github</string>
		<string>--name</string>
		<string>test</string>
	</array>
</dict>
</plist>`
	os.WriteFile(plistPath, []byte(content), 0644)

	binary := parseBinaryFromPlist(plistPath)
	if binary != "sekia-github" {
		t.Errorf("parseBinaryFromPlist = %q, want %q", binary, "sekia-github")
	}
}

func TestList(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create the LaunchAgents directory with a test plist.
	laDir := filepath.Join(tmpDir, "Library", "LaunchAgents")
	os.MkdirAll(laDir, 0755)

	plistContent := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.sekia.github-work</string>
	<key>ProgramArguments</key>
	<array>
		<string>/opt/homebrew/bin/sekia-github</string>
		<string>--name</string>
		<string>github-work</string>
	</array>
</dict>
</plist>`
	os.WriteFile(filepath.Join(laDir, "com.sekia.github-work.plist"), []byte(plistContent), 0644)

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
