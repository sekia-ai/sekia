package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
)

func testLogger() zerolog.Logger {
	return zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
}

func TestLoadSkill(t *testing.T) {
	dir := t.TempDir()
	skillMD := `---
name: pr-review
description: Reviews GitHub PRs for code quality
triggers:
  - github.pr.opened
  - github.pr.updated
version: "1.0"
---
When reviewing a PR:
1. Check for test coverage
2. Look for security issues`

	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0644); err != nil {
		t.Fatal(err)
	}

	skill, err := LoadSkill(dir)
	if err != nil {
		t.Fatalf("LoadSkill() error: %v", err)
	}

	if skill.Name != "pr-review" {
		t.Errorf("name = %q, want %q", skill.Name, "pr-review")
	}
	if skill.Description != "Reviews GitHub PRs for code quality" {
		t.Errorf("description = %q", skill.Description)
	}
	if len(skill.Triggers) != 2 {
		t.Errorf("triggers = %v, want 2 entries", skill.Triggers)
	}
	if skill.Version != "1.0" {
		t.Errorf("version = %q, want %q", skill.Version, "1.0")
	}
	if skill.Instructions != "When reviewing a PR:\n1. Check for test coverage\n2. Look for security issues" {
		t.Errorf("instructions = %q", skill.Instructions)
	}
	if skill.HasHandler {
		t.Error("expected HasHandler = false")
	}
}

func TestLoadSkill_WithHandler(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: test\n---\nInstructions"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "handler.lua"), []byte("-- handler"), 0644); err != nil {
		t.Fatal(err)
	}

	skill, err := LoadSkill(dir)
	if err != nil {
		t.Fatalf("LoadSkill() error: %v", err)
	}
	if !skill.HasHandler {
		t.Error("expected HasHandler = true")
	}
}

func TestLoadSkill_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("Just instructions, no frontmatter."), 0644); err != nil {
		t.Fatal(err)
	}

	skill, err := LoadSkill(dir)
	if err != nil {
		t.Fatalf("LoadSkill() error: %v", err)
	}
	if skill.Instructions != "Just instructions, no frontmatter." {
		t.Errorf("instructions = %q", skill.Instructions)
	}
}

func TestLoadSkillsDir(t *testing.T) {
	dir := t.TempDir()

	// Create two skill directories.
	for _, name := range []string{"alpha", "beta"} {
		skillDir := filepath.Join(dir, name)
		os.MkdirAll(skillDir, 0755)
		os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Skill "+name+"\n---\nInstructions for "+name), 0644)
	}

	// Create a non-skill directory (no SKILL.md).
	os.MkdirAll(filepath.Join(dir, "not-a-skill"), 0755)

	// Create a regular file (not a directory).
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not a skill"), 0644)

	skills, err := LoadSkillsDir(dir)
	if err != nil {
		t.Fatalf("LoadSkillsDir() error: %v", err)
	}
	if len(skills) != 2 {
		t.Errorf("got %d skills, want 2", len(skills))
	}
}

func TestLoadSkillsDir_NotExists(t *testing.T) {
	skills, err := LoadSkillsDir("/nonexistent/skills")
	if err != nil {
		t.Fatalf("LoadSkillsDir() error: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("got %d skills, want 0", len(skills))
	}
}

func TestBuildSkillsIndex(t *testing.T) {
	skills := []*Skill{
		{Name: "pr-review", Description: "Reviews PRs", Triggers: []string{"github.pr.opened"}},
		{Name: "triage", Description: "Triages issues"},
	}

	index := BuildSkillsIndex(skills)
	if index == "" {
		t.Fatal("expected non-empty index")
	}
	if !contains(index, "pr-review") || !contains(index, "triage") {
		t.Errorf("index missing skill names: %q", index)
	}
	if !contains(index, "github.pr.opened") {
		t.Errorf("index missing triggers: %q", index)
	}
}

func TestBuildSkillsIndex_Empty(t *testing.T) {
	index := BuildSkillsIndex(nil)
	if index != "" {
		t.Errorf("expected empty index, got %q", index)
	}
}

func TestManager_FullInstructions(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test-skill\ndescription: A test\n---\nDo the thing."), 0644)

	mgr := NewManager(dir, testLogger())
	if err := mgr.LoadAll(); err != nil {
		t.Fatalf("LoadAll() error: %v", err)
	}

	instructions := mgr.FullInstructions("test-skill")
	if instructions != "Do the thing." {
		t.Errorf("instructions = %q, want %q", instructions, "Do the thing.")
	}

	// Unknown skill returns empty.
	if got := mgr.FullInstructions("unknown"); got != "" {
		t.Errorf("unknown skill instructions = %q, want empty", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsHelper(s, substr)
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
