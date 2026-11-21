package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jfricker/cleanup/internal/testfix"
)

func mustLoad(t *testing.T, user string) *Config {
	t.Helper()
	cfg, err := LoadBytes("user.toml", []byte(user))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

func loadErr(t *testing.T, user string) string {
	t.Helper()
	_, err := LoadBytes("user.toml", []byte(user))
	if err == nil {
		t.Fatalf("want error for:\n%s", user)
	}
	return err.Error()
}

func TestDefaultsLoad(t *testing.T) {
	cfg, err := LoadBytes("", nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range cfg.Categories {
		ids = append(ids, c.ID)
	}
	want := []string{"python", "node", "rust", "swift", "gradle", "cmake", "test-artifacts", "macos"}
	if !slices.Equal(ids, want) {
		t.Fatalf("categories %v, want %v", ids, want)
	}
	if cfg.Categories[7].Enabled {
		t.Fatal("macos must be disabled by default")
	}
	if !cfg.Worktrees.Enabled || cfg.Worktrees.AgeDays != 30 {
		t.Fatalf("worktrees %+v", cfg.Worktrees)
	}
	if len(cfg.Retired.Tools) != 11 {
		t.Fatalf("tools %d", len(cfg.Retired.Tools))
	}
	for _, tool := range cfg.Retired.Tools {
		if tool.Retired || tool.AgeDays != 30 {
			t.Fatalf("tool %+v: want retired=false age=30", tool)
		}
	}
	for _, c := range cfg.Categories {
		for _, r := range c.Rules {
			if r.Kind == KindInvalid || r.Strategy == StrategyInvalid || r.OptIn == OptInInvalid {
				t.Fatalf("%s/%s has an invalid enum", c.ID, r.ID)
			}
		}
	}
	if cfg.NeverTouch[0] != ".git" {
		t.Fatalf("never_touch %v", cfg.NeverTouch)
	}
}

// AC-R5: a misspelled marker key is a load error with file, line and suggestion.
func TestAC_R5_MisspelledKey(t *testing.T) {
	msg := loadErr(t, testfix.R5Config)
	for _, want := range []string{"user.toml:9:", "require_sibling_marker", "unknown key", "did you mean require_sibling_markers?"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

// AC-F1: version is required.
func TestAC_F1_Version(t *testing.T) {
	msg := loadErr(t, "[global]\nnever_touch = []\n")
	if !strings.Contains(msg, "missing `version = 1`") || !strings.Contains(msg, "--print-config") {
		t.Fatalf("got %q", msg)
	}
	if msg := loadErr(t, "version = 2\n"); !strings.Contains(msg, "user.toml:1:") || !strings.Contains(msg, "unsupported version 2") {
		t.Fatalf("got %q", msg)
	}
}

// AC-F2: bad enums and over-broad or path-like patterns fail with file and line.
func TestAC_F2_BadValues(t *testing.T) {
	cases := map[string]string{
		"kind":       `kind = "folder"`,
		"strategy":   `kind = "dir"` + "\nstrategy = \"nuke\"",
		"opt_in":     `kind = "dir"` + "\nopt_in = \"always\"",
		"star":       `kind = "dir"` + "\nnames = [\"*\"]",
		"doublestar": `kind = "dir"` + "\nglobs = [\"**\"]",
		"slash":      `kind = "dir"` + "\nnames = [\"a/b\"]",
		"git":        `kind = "dir"` + "\nglobs = [\".g?t\"]",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(body, "names") && !strings.Contains(body, "globs") {
				body += "\nnames = [\"x\"]"
			}
			user := "version = 1\n[categories.rust]\n[[categories.rust.rules]]\nid = \"r\"\n" + body + "\n"
			msg := loadErr(t, user)
			if !strings.Contains(msg, "user.toml:") || !strings.Contains(msg, "[id=r]") {
				t.Fatalf("want file:line and rule id, got %q", msg)
			}
			if strings.Contains(msg, "user.toml:0") {
				t.Fatalf("line missing: %q", msg)
			}
		})
	}
}

// AC-F3: the printed defaults load as a user config.
func TestAC_F3_RoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, Defaults(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
}

// AC-F4: .git can't be removed from never_touch.
func TestAC_F4_GitAlwaysNeverTouch(t *testing.T) {
	cfg := mustLoad(t, "version = 1\n[global]\nnever_touch = [\"vendor\"]\n")
	if !slices.Equal(cfg.NeverTouch, []string{".git", "vendor"}) {
		t.Fatalf("never_touch %v", cfg.NeverTouch)
	}
}

func TestMergeSemantics(t *testing.T) {
	cfg := mustLoad(t, `version = 1
[providers.worktrees]
age_days = 7

[categories.rust]
label = "Cargo"

[categories.node]
[[categories.node.rules]]
id = "only"
kind = "dir"
names = [".next"]

[categories.custom]
enabled = true
sort_order = 5
[[categories.custom.rules]]
id = "bazel"
kind = "dir"
names = ["bazel-out"]

[retired_tools]
age_days = 60
[[retired_tools.tools]]
id = "cursor"
retired = true
[[retired_tools.tools]]
id = "mytool"
dirs = [".mytool"]
`)
	if cfg.Worktrees.AgeDays != 7 || !cfg.Worktrees.Enabled || cfg.Worktrees.Label != "Git Worktrees" {
		t.Fatalf("worktrees %+v", cfg.Worktrees)
	}
	cat := func(id string) Category {
		i := slices.IndexFunc(cfg.Categories, func(c Category) bool { return c.ID == id })
		if i < 0 {
			t.Fatalf("no category %s", id)
		}
		return cfg.Categories[i]
	}
	if r := cat("rust"); r.Label != "Cargo" || len(r.Rules) != 1 || !r.Enabled {
		t.Fatalf("rust %+v", r)
	}
	if n := cat("node"); len(n.Rules) != 1 || n.Rules[0].ID != "only" || n.Rules[0].Strategy != StrategyRemove {
		t.Fatalf("node rules not replaced: %+v", n.Rules)
	}
	if cfg.Categories[0].ID != "custom" {
		t.Fatalf("sort order: first is %s", cfg.Categories[0].ID)
	}
	var cursor, mine Tool
	for _, tool := range cfg.Retired.Tools {
		switch tool.ID {
		case "cursor":
			cursor = tool
		case "mytool":
			mine = tool
		}
	}
	if !cursor.Retired || cursor.AgeDays != 60 || len(cursor.Files) != 3 {
		t.Fatalf("cursor %+v", cursor)
	}
	if mine.Name != "mytool" || mine.AgeDays != 60 {
		t.Fatalf("mytool %+v", mine)
	}
}

// RET-3: retired tools must keep an age gate of at least one day.
func TestRET3_AgeGate(t *testing.T) {
	msg := loadErr(t, "version = 1\n[[retired_tools.tools]]\nid = \"cursor\"\nage_days = 0\n")
	if !strings.Contains(msg, "user.toml:4:") || !strings.Contains(msg, ">= 1") {
		t.Fatalf("got %q", msg)
	}
	loadErr(t, "version = 1\n[retired_tools]\nage_days = 0\n")
}

func TestUnknownTable(t *testing.T) {
	_, err := LoadBytes("old.toml", []byte("version = 1\n[defunct_tools]\nenabled = true\n"))
	var le *LoadError
	if !errors.As(err, &le) || le.Line != 2 || le.Key != "defunct_tools" || le.Hint != "" && !strings.Contains(le.Hint, "retired") {
		t.Fatalf("got %#v", err)
	}
}

func TestParseErrorHasLine(t *testing.T) {
	msg := loadErr(t, "version = 1\n[global\n")
	if !strings.HasPrefix(msg, "user.toml:2") && !strings.HasPrefix(msg, "user.toml:3") {
		t.Fatalf("got %q", msg)
	}
}
