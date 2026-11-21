package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfricker/cleanup/internal/cli"
	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/gitx"
	"github.com/jfricker/cleanup/internal/match"
	"github.com/jfricker/cleanup/internal/plain"
	"github.com/jfricker/cleanup/internal/rootguard"
	"github.com/jfricker/cleanup/internal/scan"
	"github.com/jfricker/cleanup/internal/testfix"
)

const hintLine = `cleanup: no config yet; run "cleanup --init" to set your project root and retired tools` + "\n"

// inProc runs run() with a fake env whose XDG_CONFIG_HOME is cfgHome.
func inProc(t *testing.T, cfgHome string, interactive bool, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var out, errb bytes.Buffer
	e := env{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Interactive: func() bool { return interactive },
		Getenv: func(k string) string {
			if k == "XDG_CONFIG_HOME" {
				return cfgHome
			}
			return ""
		},
		Executable: func() (string, error) { return "cleanup", nil },
		Now:        time.Now,
	}
	code = run(args, e)
	return code, out.String(), errb.String()
}

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "config.toml")
	testfix.Write(t, f, body)
	return f
}

func TestAC_P1_ProjectRootAndFlagOverride(t *testing.T) {
	a, b := e2eRoot(t), e2eRoot(t)
	testfix.Write(t, filepath.Join(a, "alpha/Cargo.toml"), "[package]\n")
	testfix.Write(t, filepath.Join(a, "alpha/target/debug/x"), "12345")
	testfix.Write(t, filepath.Join(b, "beta/Cargo.toml"), "[package]\n")
	testfix.Write(t, filepath.Join(b, "beta/target/debug/x"), "12345")
	cfg := writeCfg(t, "version = 1\nproject_root = \""+a+"\"\n")

	code, out, errs := inProc(t, t.TempDir(), false, "-T", "-c", cfg)
	if code != 0 || !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("config root: %d\n%s\n%s", code, out, errs)
	}
	code, out, errs = inProc(t, t.TempDir(), false, "-T", "-c", cfg, "-r", b)
	if code != 0 || !strings.Contains(out, "beta") || strings.Contains(out, "alpha") {
		t.Fatalf("flag root: %d\n%s\n%s", code, out, errs)
	}
	code, out, _ = inProc(t, t.TempDir(), false, "-T", "-c", cfg, b) // positional
	if code != 0 || !strings.Contains(out, "beta") {
		t.Fatalf("positional root: %d\n%s", code, out)
	}
}

func TestAC_P3_StaleProjectRootErrors(t *testing.T) {
	gone := e2eRoot(t)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	for name, root := range map[string]string{"deleted": gone, "home": "~"} {
		cfg := writeCfg(t, "version = 1\nproject_root = \""+root+"\"\n")
		code, out, errs := inProc(t, t.TempDir(), false, "-T", "-c", cfg)
		if code != 2 || out != "" {
			t.Fatalf("%s: %d %q", name, code, out)
		}
		for _, want := range []string{"project_root ", "from " + cfg, `run "cleanup --init" or pass -r DIR`} {
			if !strings.Contains(errs, want) {
				t.Errorf("%s: stderr missing %q: %s", name, want, errs)
			}
		}
		if code, _, _ := inProc(t, t.TempDir(), false, "-l", "-c", cfg); code != 2 {
			t.Errorf("%s -l: exit %d", name, code)
		}
	}
	// A file is not a directory.
	f := filepath.Join(e2eRoot(t), "file")
	testfix.Write(t, f, "x")
	cfg := writeCfg(t, "version = 1\nproject_root = \""+f+"\"\n")
	if code, _, errs := inProc(t, t.TempDir(), false, "-c", cfg); code != 2 || !strings.Contains(errs, "not a directory") {
		t.Errorf("file: %d %s", code, errs)
	}
	// -r bypasses the stale value.
	if code, _, errs := inProc(t, t.TempDir(), false, "-T", "-c", cfg, "-r", e2eRoot(t)); code != 0 {
		t.Errorf("-r bypass: %d %s", code, errs)
	}
}

func TestAC_P4_MissingProtectedListedInL(t *testing.T) {
	root := e2eRoot(t)
	testfix.Mkdir(t, filepath.Join(root, "keep"))
	cfg := writeCfg(t, "version = 1\nprotected_paths = [\"keep\", \"gone/dir\"]\n")
	code, out, errs := inProc(t, t.TempDir(), false, "-l", "-c", cfg, "-r", root)
	if code != 0 {
		t.Fatalf("%d %s", code, errs)
	}
	for _, want := range []string{"Protected: keep ok\n", "Protected: gone/dir missing\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestAC_P8_ListShowsRootSourceAndProtected(t *testing.T) {
	root := e2eRoot(t)
	cfgRoot := e2eRoot(t)
	cfg := writeCfg(t, "version = 1\nproject_root = \""+cfgRoot+"\"\nprotected_paths = [\"p\"]\n")
	_, out, _ := inProc(t, t.TempDir(), false, "-l", "-c", cfg)
	if !strings.HasPrefix(out, "Root: "+cfgRoot+" (from config)\nProtected: p missing\n") {
		t.Errorf("config:\n%s", out)
	}
	_, out, _ = inProc(t, t.TempDir(), false, "-l", "-c", cfg, "-r", root)
	if !strings.HasPrefix(out, "Root: "+root+" (from flag)\n") {
		t.Errorf("flag:\n%s", out)
	}
	home, _ := rootguard.Home()
	_, out, _ = inProc(t, t.TempDir(), false, "-l")
	if !strings.HasPrefix(out, "Root: "+filepath.Join(home, "Projects")+" (from default)\n") {
		t.Errorf("default:\n%s", out)
	}
}

func TestAC_P7_ConfigChangeBeforeDeleteRefuses(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := e2eRoot(t)
	testfix.Write(t, filepath.Join(root, "rs/Cargo.toml"), "[package]\n")
	testfix.Write(t, filepath.Join(root, "rs/target/debug/app"), "12345")
	cfgPath := writeCfg(t, "version = 1\n")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	rg, err := rootguard.Resolve(root, rootguard.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer rg.Close()
	git, err := gitx.New(gitx.Options{})
	if err != nil {
		t.Fatal(err)
	}
	so := scan.Options{OptIns: match.OptIns{}, Protected: cfg.ProtectedPaths}
	sig := newSignals()
	defer sig.stop()
	s := &session{opts: cli.Options{Mode: cli.Delete}, cfg: cfg, root: rg, git: git, scanOpts: so,
		cfgPath: cfgPath, cleaners: scan.Active(cfg, so), stdout: io.Discard, stderr: io.Discard, signals: sig}
	res, err := scan.Run(context.Background(), cfg, rg, git, so, nil)
	if err != nil {
		t.Fatal(err)
	}
	eligible := plain.Printer{W: io.Discard}.Scan(res, s.cleaners, root, true)
	if len(eligible) == 0 {
		t.Fatal("nothing eligible")
	}
	testfix.Write(t, cfgPath, "version = 1\nprotected_paths = [\"rs\"]\n")
	rep, _, code := s.execute(res, eligible, nil)
	if rep == nil || code != 0 || len(rep.Deleted) != 0 || len(rep.Skipped) != len(eligible) {
		t.Fatalf("report: %+v code %d", rep, code)
	}
	if r := rep.Skipped[0].Outcome.Reason; r == nil || !strings.Contains(r.String(), "protected path") {
		t.Fatalf("reason: %+v", rep.Skipped[0].Outcome)
	}
	if !testfix.Exists(filepath.Join(root, "rs/target")) {
		t.Fatal("target was deleted")
	}

	// A config that no longer loads aborts with nothing deleted.
	testfix.Write(t, cfgPath, "version = 1\nbogus_key = 1\n")
	var errb bytes.Buffer
	s.stderr = &errb
	if rep, _, code := s.execute(res, eligible, nil); rep != nil || code != 2 || !strings.Contains(errb.String(), "nothing was deleted") {
		t.Fatalf("bad reload: %v %d %q", rep, code, errb.String())
	}
	if !testfix.Exists(filepath.Join(root, "rs/target")) {
		t.Fatal("target was deleted")
	}
}

// pipeStdin returns a stdin pipe that is never written or closed until cleanup, so
// any read would block the run.
func pipeStdin(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	return r
}

func TestAC_N3_NonInteractiveHint(t *testing.T) {
	root := e2eRoot(t)
	testfix.Write(t, filepath.Join(root, "rs/Cargo.toml"), "[package]\n")
	testfix.Write(t, filepath.Join(root, "rs/target/debug/app"), "12345")
	testfix.Write(t, filepath.Join(root, "py/__pycache__/m.pyc"), "1234567890")
	testfix.Write(t, filepath.Join(root, "notrust/target/important.db"), "data")
	testfix.ScenarioR3(t, root)
	r := runBinIn(t, pipeStdin(t), "-T", "-s", "-r", root)
	if r.code != 0 || r.stderr != hintLine {
		t.Fatalf("%d stderr %q", r.code, r.stderr)
	}
	golden(t, "dryrun", strings.ReplaceAll(r.stdout, root, "$ROOT"))
}

func TestAC_N4_ForceDeleteNoPrompt(t *testing.T) {
	root := e2eRoot(t)
	r := runBinIn(t, pipeStdin(t), "-f", "-d", "-r", root)
	if r.code != 0 || r.stderr != hintLine {
		t.Fatalf("%d stderr %q stdout %q", r.code, r.stderr, r.stdout)
	}
}

func TestAC_N5_InfoFlagsNoHint(t *testing.T) {
	for _, f := range []string{"-P", "-M", "-l", "-h", "-V"} {
		r := runBinIn(t, pipeStdin(t), f)
		if r.code != 0 || r.stderr != "" {
			t.Errorf("%s: %d stderr %q", f, r.code, r.stderr)
		}
	}
}

func TestAC_N6_MissingDashCIsConfigError(t *testing.T) {
	r := runBinIn(t, pipeStdin(t), "-c", filepath.Join(t.TempDir(), "missing.toml"), "-r", e2eRoot(t))
	if r.code != 2 || !strings.Contains(r.stderr, "config error") || strings.Contains(r.stderr, "--init") {
		t.Fatalf("%d %q", r.code, r.stderr)
	}
}

func TestDecideStart(t *testing.T) {
	for _, c := range []struct {
		name        string
		o           cli.Options
		exists, tty bool
		want        start
	}{
		{"no default, non-interactive", cli.Options{}, false, false, startHint},
		{"no default, interactive", cli.Options{}, false, true, startOnboardOnly},
		{"interactive -d still onboards", cli.Options{Mode: cli.Delete}, false, true, startOnboardOnly},
		{"interactive -f hints", cli.Options{Force: true}, false, true, startHint},
		{"interactive -T hints", cli.Options{NoTUI: true}, false, true, startHint},
		{"--init", cli.Options{Init: true}, true, true, startOnboard},
		{"default exists", cli.Options{}, true, false, startRun},
		{"explicit -c", cli.Options{ConfigPath: "x"}, false, true, startRun},
		{"-l never hints", cli.Options{ListCategories: true}, false, false, startRun},
		{"-f -d hints", cli.Options{Force: true, Mode: cli.Delete}, false, false, startHint},
	} {
		if got := decideStart(c.o, c.exists, c.tty); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestResolveRoot(t *testing.T) {
	dir := e2eRoot(t)
	file := filepath.Join(dir, "f")
	testfix.Write(t, file, "x")
	home := "/home/x"
	for _, c := range []struct {
		name    string
		o       cli.Options
		cfg     config.Config
		want    string
		src     rootSource
		wantErr string
	}{
		{"default", cli.Options{}, config.Config{}, "/home/x/Projects", rootFromDefault, ""},
		{"config", cli.Options{}, config.Config{ProjectRoot: dir}, dir, rootFromConfig, ""},
		{"flag beats config", cli.Options{Root: "/elsewhere"}, config.Config{ProjectRoot: "/nope"}, "/elsewhere", rootFromFlag, ""},
		{"config missing", cli.Options{}, config.Config{ProjectRoot: dir + "/nope"}, "", "", "no such file"},
		{"config is file", cli.Options{}, config.Config{ProjectRoot: file}, "", "", "not a directory"},
	} {
		got, src, err := resolveRoot(c.o, &c.cfg, "cfg.toml", home)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) || !strings.Contains(err.Error(), "from cfg.toml") {
				t.Errorf("%s: err %v", c.name, err)
			}
			continue
		}
		if err != nil || got != c.want || src != c.src {
			t.Errorf("%s: %q %q %v", c.name, got, src, err)
		}
	}
}
