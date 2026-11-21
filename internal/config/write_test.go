package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const k1Existing = `# my comment
version = 1

[global]
never_touch = [".git", "keepme"]

[categories.custom]
label = "Custom"
prune = ["x"]

[[categories.custom.rules]]
id = "c1"
category = "custom"
kind = "dir"
names = ["foo"]
`

func TestAC_K1_KeepsUserKeysDropsComments(t *testing.T) {
	out, err := Plan([]byte(k1Existing), Changes{Retired: map[string]bool{"orca": true}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "my comment") {
		t.Error("comment survived")
	}
	cfg, err := LoadBytes("config.toml", out)
	if err != nil {
		t.Fatalf("output must load strictly: %v\n%s", err, out)
	}
	if !slices.Contains(cfg.NeverTouch, "keepme") {
		t.Errorf("never_touch lost: %v", cfg.NeverTouch)
	}
	found := false
	for _, c := range cfg.Categories {
		if c.ID == "custom" && len(c.Rules) == 1 {
			found = true
		}
	}
	if !found {
		t.Error("custom category lost")
	}
	for _, tl := range cfg.Retired.Tools {
		if tl.ID == "orca" && !tl.Retired {
			t.Error("orca not retired")
		}
	}
	if strings.Contains(string(out), "opt_in = \"\"") || strings.Contains(string(out), "warning = \"\"") {
		t.Errorf("empty keys added:\n%s", out)
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(k1Existing), 0o600); err != nil {
		t.Fatal(err)
	}
	bak, err := Write(path, out, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bak); string(b) != k1Existing {
		t.Error("backup differs from original")
	}
	if b, _ := os.ReadFile(path); string(b) != string(out) {
		t.Error("target not replaced")
	}
}

func TestAC_K2_DistinctBackupsSameSecond(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	seen := map[string]bool{}
	for i := range 3 {
		if i == 0 {
			if err := os.WriteFile(path, []byte("version = 1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		bak, err := Write(path, []byte("version = 1\n"), now)
		if err != nil {
			t.Fatal(err)
		}
		if seen[bak] {
			t.Fatalf("duplicate backup %s", bak)
		}
		seen[bak] = true
	}
	for _, want := range []string{"config.toml.bak-20260102T030405", "config.toml.bak-20260102T030405-2", "config.toml.bak-20260102T030405-3"} {
		if !seen[filepath.Join(filepath.Dir(path), want)] {
			t.Errorf("missing %s: %v", want, seen)
		}
	}
}

func TestAC_K3_UnretireDropsOrTrimsEntry(t *testing.T) {
	out, err := Plan(nil, Changes{Retired: map[string]bool{"orca": true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "retired = true") {
		t.Fatalf("expected retired entry:\n%s", out)
	}
	out, err = Plan(out, Changes{Retired: map[string]bool{"orca": false}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "version = 1" {
		t.Errorf("entry not dropped:\n%s", out)
	}
	// An entry with other fields keeps them and loses only retired.
	in := "version = 1\n\n[[retired_tools.tools]]\nid = \"orca\"\nretired = true\nnote = \"mine\"\n"
	out, err = Plan([]byte(in), Changes{Retired: map[string]bool{"orca": false}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "retired =") || !strings.Contains(s, `note = "mine"`) {
		t.Errorf("entry not trimmed:\n%s", s)
	}
}

func TestAC_K4_FailedValidationWritesNothing(t *testing.T) {
	out, err := Plan([]byte("version = 1\n"), Changes{ProjectRoot: "relative/dir"})
	if err == nil || out != nil {
		t.Fatalf("want error and no bytes, got %q, %v", out, err)
	}
	if _, err := Plan([]byte("version = 1\nbogus = 1\n"), Changes{}); err == nil {
		t.Error("invalid existing file must error")
	}
}

func TestAC_K5_CrashBeforeRenameKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	orig := []byte("version = 1\n")
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("crash")
	writeHooks.beforeRename = func() error { return boom }
	t.Cleanup(func() { writeHooks.beforeRename = nil })
	_, err := Write(path, []byte("version = 1\nproject_root = \"~/Code\"\n"), time.Now())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(orig) {
		t.Errorf("target changed: %q", b)
	}
	// The next write reuses the leftover temp file.
	writeHooks.beforeRename = nil
	if _, err := Write(path, []byte("version = 1\n"), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestAC_K7_Modes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newdir")
	path := filepath.Join(dir, "config.toml")
	if _, err := Write(path, []byte("version = 1\n"), time.Now()); err != nil {
		t.Fatal(err)
	}
	di, _ := os.Stat(dir)
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", di.Mode().Perm())
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", fi.Mode().Perm())
	}
	bak, err := Write(path, []byte("version = 1\n"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if bi, _ := os.Stat(bak); bi.Mode().Perm() != 0o600 {
		t.Errorf("backup mode %v", bi.Mode().Perm())
	}
}

func TestPlanEmptyAndDefaultRoot(t *testing.T) {
	out, err := Plan(nil, Changes{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "version = 1" {
		t.Errorf("got:\n%s", out)
	}
	if _, err := LoadBytes("config.toml", out); err != nil {
		t.Fatal(err)
	}
	out, err = Plan([]byte("version = 1\nproject_root = \"~/Code\"\n"), Changes{ProjectRoot: "~/Projects"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "project_root") {
		t.Errorf("default root not omitted:\n%s", out)
	}
	out, err = Plan(nil, Changes{ProjectRoot: "~/Code", ProtectedPaths: []string{"a/b"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadBytes("config.toml", out)
	if err != nil || !slices.Contains(cfg.ProtectedPaths, "a/b") {
		t.Fatalf("%v %v", cfg, err)
	}
}
