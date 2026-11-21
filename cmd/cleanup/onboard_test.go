package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/testfix"
)

// onbEnv is an in-process fixture: a temp home (inside the repo, since temp dirs under
// /private are refused as roots) with a Projects directory and an empty config home.
type onbEnv struct {
	home, cfgHome string
	getenv        map[string]string
	exe           string
	gitBin        string
}

func newOnbEnv(t *testing.T) *onbEnv {
	t.Helper()
	home := e2eRoot(t)
	testfix.Mkdir(t, filepath.Join(home, "Projects"))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	binDir := t.TempDir()
	return &onbEnv{
		home: home, cfgHome: filepath.Join(home, "cfg"),
		getenv: map[string]string{"TERM": "dumb", "PATH": binDir},
		exe:    filepath.Join(binDir, "cleanup"),
	}
}

func (o *onbEnv) defaultCfg() string { return filepath.Join(o.cfgHome, "cleanup", "config.toml") }

func (o *onbEnv) run(interactive bool, stdin string, args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	e := env{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb,
		Interactive: func() bool { return interactive },
		Getenv: func(k string) string {
			if k == "XDG_CONFIG_HOME" {
				return o.cfgHome
			}
			return o.getenv[k]
		},
		Executable: func() (string, error) { return o.exe, nil },
		Now:        time.Now,
		GitBin:     o.gitBin,
		Home:       o.home,
	}
	code = run(args, e)
	return code, out.String(), errb.String()
}

const acceptAll = "\n\n\n\n\n"

func TestAC_N1_FirstRunStartsOnboardingNoScan(t *testing.T) {
	o := newOnbEnv(t)
	root := filepath.Join(o.home, "Projects")
	testfix.Write(t, filepath.Join(root, "rs/Cargo.toml"), "[package]\n")
	testfix.Write(t, filepath.Join(root, "rs/target/debug/app"), "12345")
	code, out, errs := o.run(true, acceptAll)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if !strings.Contains(out, "Step 1 of 4") || !strings.Contains(out, "Config written:") {
		t.Fatalf("no onboarding:\n%s", out)
	}
	if strings.Contains(out, "rs/target") || strings.Contains(errs, "no config yet") {
		t.Fatalf("scanned or hinted:\n%s\n%s", out, errs)
	}
	if !strings.Contains(out, "run your command again") {
		t.Fatalf("missing auto-trigger note:\n%s", out)
	}
	if _, err := os.Stat(o.defaultCfg()); err != nil {
		t.Fatal(err)
	}
}

func TestAC_N2_DeleteFlagOnboardingOnlyNoDeletion(t *testing.T) {
	o := newOnbEnv(t)
	root := filepath.Join(o.home, "Projects")
	tgt := filepath.Join(root, "rs/target/debug/app")
	testfix.Write(t, filepath.Join(root, "rs/Cargo.toml"), "[package]\n")
	testfix.Write(t, tgt, "12345")
	code, out, errs := o.run(true, acceptAll, "-d")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if !testfix.Exists(tgt) || strings.Contains(out, "Deleted") {
		t.Fatalf("deleted something:\n%s", out)
	}
	if !strings.Contains(out, "Config written:") {
		t.Fatalf("no onboarding:\n%s", out)
	}
}

func TestAC_N7_InitNonInteractive(t *testing.T) {
	o := newOnbEnv(t)
	code, out, errs := o.run(false, "", "--init")
	if code != 2 || !strings.Contains(errs, "--init needs an interactive terminal") || out != "" {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if testfix.Exists(o.defaultCfg()) {
		t.Fatal("config written")
	}
}

func TestAC_N8_InitFlagConflicts(t *testing.T) {
	o := newOnbEnv(t)
	for flag, name := range map[string]string{"-d": "delete", "-f": "force", "-P": "print-config"} {
		code, _, errs := o.run(true, "", "--init", flag)
		if code != 2 || !strings.Contains(errs, "--init cannot be combined with --"+name) {
			t.Errorf("%s: %d %q", flag, code, errs)
		}
	}
	cfg := filepath.Join(o.home, "other.toml")
	code, out, errs := o.run(true, acceptAll, "--init", "-c", cfg, "-r", filepath.Join(o.home, "Projects"), "-T")
	if code != 0 {
		t.Fatalf("accepted combination: %d\n%s\n%s", code, out, errs)
	}
	if code, _, errs := o.run(true, "", "--init", "-r", o.home, o.home); code != 2 {
		t.Errorf("positional plus -r: %d %q", code, errs)
	}
}

func TestAC_N9_HelpManListInit(t *testing.T) {
	for _, g := range []string{"help", "man"} {
		b, err := os.ReadFile(filepath.Join("testdata", g+".golden"))
		if err != nil || !strings.Contains(string(b), "init") {
			t.Errorf("%s golden lacks --init (%v)", g, err)
		}
	}
	code, out, _ := newOnbEnv(t).run(false, "", "-h")
	if code != 0 || !strings.Contains(out, "--init") {
		t.Fatalf("help: %d", code)
	}
}

func TestAC_O1_AcceptDefaultsWritesVersionOnly(t *testing.T) {
	o := newOnbEnv(t)
	code, out, errs := o.run(true, acceptAll, "--init")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	b, err := os.ReadFile(o.defaultCfg())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "version = 1" {
		t.Fatalf("file:\n%s", b)
	}
	if _, err := config.Load(o.defaultCfg()); err != nil {
		t.Fatal(err)
	}
}

func TestAC_O3_InitDashRPrefill(t *testing.T) {
	o := newOnbEnv(t)
	dir := filepath.Join(o.home, "work")
	testfix.Mkdir(t, dir)
	code, out, errs := o.run(true, "\n", "--init", "-r", dir)
	if !strings.Contains(out, "Directory to scan ["+dir+"]") {
		t.Fatalf("prompt lacks prefill (%d):\n%s\n%s", code, out, errs)
	}
	code, out, _ = o.run(true, "\n", "--init", dir) // positional acts like -r
	if !strings.Contains(out, "Directory to scan ["+dir+"]") {
		t.Fatalf("positional (%d):\n%s", code, out)
	}
}

func TestAC_O7_PathWarning(t *testing.T) {
	bin := t.TempDir()
	exe := func() (string, error) { return filepath.Join(bin, "cleanup"), nil }
	if w := pathWarning(exe, "/usr/bin:"+bin, "/bin/zsh"); w != "" {
		t.Fatalf("on PATH: %q", w)
	}
	canon := testfix.Canonical(t, bin)
	w := pathWarning(exe, "/usr/bin", "/bin/zsh")
	if !strings.Contains(w, canon) || !strings.Contains(w, `export PATH="`+canon+`:$PATH"`) || !strings.Contains(w, "~/.zshrc") {
		t.Fatalf("zsh: %q", w)
	}
	if w := pathWarning(exe, "/usr/bin", "/bin/bash"); !strings.Contains(w, "~/.bashrc") {
		t.Fatalf("bash: %q", w)
	}
	if w := pathWarning(exe, "/usr/bin", "/usr/bin/fish"); !strings.Contains(w, "fish_add_path "+canon) {
		t.Fatalf("fish: %q", w)
	}

	o := newOnbEnv(t)
	o.getenv["PATH"] = "/usr/bin"
	o.getenv["SHELL"] = "/bin/zsh"
	_, out, _ := o.run(true, "", "--init")
	if !strings.HasPrefix(out, "Warning: ") || !strings.Contains(out, "export PATH=") {
		t.Fatalf("no warning in plain output:\n%s", out)
	}
}

func TestAC_O8_TermDumbUsesPlain(t *testing.T) {
	o := newOnbEnv(t) // TERM=dumb, no -T
	code, out, errs := o.run(true, acceptAll, "--init")
	if code != 0 || !strings.Contains(out, "Step 1 of 4") || strings.Contains(out, "\x1b") {
		t.Fatalf("%d\n%q\n%s", code, out, errs)
	}
}

func TestAC_K6_InitDashCWritesOtherFile(t *testing.T) {
	o := newOnbEnv(t)
	other := filepath.Join(o.home, "elsewhere", "my.toml")
	code, out, errs := o.run(true, acceptAll, "--init", "-c", other)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if !testfix.Exists(other) || testfix.Exists(o.defaultCfg()) {
		t.Fatalf("other=%v default=%v", testfix.Exists(other), testfix.Exists(o.defaultCfg()))
	}
	if !strings.Contains(out, other) {
		t.Fatalf("summary lacks path:\n%s", out)
	}
}

func treeHash(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s|%d|%d|%v\n", p, fi.Size(), fi.ModTime().UnixNano(), fi.Mode())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestAC_O9_OnboardingIsReadOnly(t *testing.T) {
	o := newOnbEnv(t)
	root := filepath.Join(o.home, "Projects")
	repo := testfix.Repo(t, filepath.Join(root, "app"), "node_modules")
	testfix.Write(t, filepath.Join(repo, ".cursor/rules.md"), "x")
	testfix.Write(t, filepath.Join(repo, "node_modules/p/i.js"), "x")
	testfix.Backdate(t, filepath.Join(repo, ".cursor"), 90)
	testfix.Write(t, filepath.Join(root, "plain/.orca/state"), "1234")
	testfix.Backdate(t, filepath.Join(root, "plain/.orca"), 90)
	testfix.Worktree(t, repo, filepath.Join(root, "app-wt"), "feature")

	logFile := filepath.Join(t.TempDir(), "git.log")
	wrapper := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + logFile + "'\nexec /usr/bin/git \"$@\"\n"
	testfix.Write(t, wrapper, script)
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	o.gitBin = wrapper

	before := treeHash(t, root)
	// Root, protected, then mark every shown tool, then confirm.
	code, out, errs := o.run(true, "\n\n1 2\n\n\n", "--init", "-r", root)
	if code != 0 || !strings.Contains(out, "Step 3 of 4") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if after := treeHash(t, root); after != before {
		t.Fatalf("project tree changed during onboarding\n%s", out)
	}
	logged, _ := os.ReadFile(logFile)
	for _, line := range strings.Split(string(logged), "\n") {
		toks := strings.Fields(line)
		for i, tok := range toks {
			switch tok {
			case "add", "commit", "checkout", "reset", "prune", "gc", "update-index", "clean", "rm", "stash", "fetch", "pull", "push", "merge", "rebase", "branch", "config", "init":
				t.Errorf("write-capable git subcommand in %q", line)
			case "worktree":
				if i+1 < len(toks) && toks[i+1] != "list" {
					t.Errorf("git worktree %s in %q", toks[i+1], line)
				}
			}
		}
	}
}
