package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func sha256Hex(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

func TestLoadManifest(t *testing.T) {
	dir := t.TempDir()
	content := sha256Hex("hello") + "  hello.lua\n" + sha256Hex("world") + "  world.lua\n"
	os.WriteFile(filepath.Join(dir, ManifestFilename), []byte(content), 0644)

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil manifest")
	}
	if m.Count() != 2 {
		t.Fatalf("expected 2 entries, got %d", m.Count())
	}
}

func TestLoadManifest_NotExist(t *testing.T) {
	m, err := LoadManifest(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m != nil {
		t.Fatal("expected nil manifest for nonexistent file")
	}
}

func TestLoadManifest_Comments(t *testing.T) {
	dir := t.TempDir()
	content := "# this is a comment\n\n" + sha256Hex("data") + "  test.lua\n\n# another comment\n"
	os.WriteFile(filepath.Join(dir, ManifestFilename), []byte(content), 0644)

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Count() != 1 {
		t.Fatalf("expected 1 entry, got %d", m.Count())
	}
}

func TestLoadManifest_InvalidFormat(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ManifestFilename), []byte("not a valid line\n"), 0644)

	_, err := LoadManifest(dir)
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestLoadManifest_InvalidHex(t *testing.T) {
	dir := t.TempDir()
	// 64 chars but not valid hex
	badHash := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	os.WriteFile(filepath.Join(dir, ManifestFilename), []byte(badHash+"  test.lua\n"), 0644)

	_, err := LoadManifest(dir)
	if err == nil {
		t.Fatal("expected error for invalid hex")
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	data := "sekia.on('test', function() end)"
	path := filepath.Join(dir, "test.lua")
	os.WriteFile(path, []byte(data), 0644)

	got, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	want := sha256Hex(data)
	if got != want {
		t.Fatalf("hash = %s, want %s", got, want)
	}
}

func TestManifest_Verify_Match(t *testing.T) {
	dir := t.TempDir()
	data := "hello world"
	path := filepath.Join(dir, "test.lua")
	os.WriteFile(path, []byte(data), 0644)

	m := &Manifest{entries: map[string]string{
		"test.lua": sha256Hex(data),
	}}

	if err := m.Verify("test.lua", path); err != nil {
		t.Fatalf("Verify should pass: %v", err)
	}
}

func TestManifest_Verify_Mismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lua")
	os.WriteFile(path, []byte("actual content"), 0644)

	m := &Manifest{entries: map[string]string{
		"test.lua": sha256Hex("different content"),
	}}

	err := m.Verify("test.lua", path)
	if err == nil {
		t.Fatal("expected error for hash mismatch")
	}
}

func TestManifest_Verify_NotInManifest(t *testing.T) {
	m := &Manifest{entries: map[string]string{}}

	err := m.Verify("missing.lua", "/nonexistent")
	if err == nil {
		t.Fatal("expected error for file not in manifest")
	}
}

func TestGenerateManifest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.lua"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(dir, "b.lua"), []byte("bbb"), 0644)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not lua"), 0644)

	m, err := GenerateManifest(dir)
	if err != nil {
		t.Fatalf("GenerateManifest: %v", err)
	}
	if m.Count() != 2 {
		t.Fatalf("expected 2 entries, got %d", m.Count())
	}

	// Verify the generated hashes are correct.
	if err := m.Verify("a.lua", filepath.Join(dir, "a.lua")); err != nil {
		t.Fatalf("a.lua verify failed: %v", err)
	}
	if err := m.Verify("b.lua", filepath.Join(dir, "b.lua")); err != nil {
		t.Fatalf("b.lua verify failed: %v", err)
	}
}

func TestManifest_WriteFile_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.lua"), []byte("xxx"), 0644)
	os.WriteFile(filepath.Join(dir, "y.lua"), []byte("yyy"), 0644)

	m1, err := GenerateManifest(dir)
	if err != nil {
		t.Fatalf("GenerateManifest: %v", err)
	}
	if err := m1.WriteFile(dir); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m2, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m2.Count() != m1.Count() {
		t.Fatalf("roundtrip count mismatch: %d vs %d", m2.Count(), m1.Count())
	}

	// Verify loaded manifest still validates files.
	if err := m2.Verify("x.lua", filepath.Join(dir, "x.lua")); err != nil {
		t.Fatalf("roundtrip verify x.lua: %v", err)
	}
	if err := m2.Verify("y.lua", filepath.Join(dir, "y.lua")); err != nil {
		t.Fatalf("roundtrip verify y.lua: %v", err)
	}
}
