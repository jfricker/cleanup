package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/testfix"
)

var update = flag.Bool("update", false, "rewrite golden files")

var binPath string

func TestMain(m *testing.M) {
	flag.Parse()
	dir, err := os.MkdirTemp("", "cleanup-bin")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(dir, "cleanup")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// e2eRoot returns a scan root outside /private (refused on macOS), inside the repo.
func e2eRoot(t *testing.T) string {
	t.Helper()
	base, err := filepath.Abs("../../.e2e-tmp")
	if err != nil {
		t.Fatal(err)
	}
	testfix.Mkdir(t, base)
	dir, err := os.MkdirTemp(base, "root-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return testfix.Canonical(t, dir)
}

type result struct {
	stdout, stderr string
	code           int
	state          string
}

func runBin(t *testing.T, args ...string) result {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(testfix.Env(t), "HOME="+home, "XDG_STATE_HOME="+filepath.Join(home, "state"),
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"), "NO_COLOR=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{out.String(), errb.String(), code, filepath.Join(home, "state")}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		testfix.Write(t, path, got)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run make golden)", err)
	}
	if string(want) != got {
		t.Fatalf("%s differs from golden file; run make golden and review.\n--- got ---\n%s", name, got)
	}
}

// AC-L1: -y is rejected; --delete without a TTY or --force is a usage error.
func TestAC_L1_Flags(t *testing.T) {
	r := runBin(t, "-y")
	if r.code != 2 || !strings.Contains(r.stderr, "use -d/--delete") {
		t.Fatalf("-y: %d %q", r.code, r.stderr)
	}
	r = runBin(t, "-yf")
	if r.code != 2 {
		t.Fatalf("-yf: %d", r.code)
	}
	r = runBin(t, "-d", "-r", e2eRoot(t))
	if r.code != 2 || !strings.Contains(r.stderr, "-f/--force") {
		t.Fatalf("-d without tty: %d %q", r.code, r.stderr)
	}
	if r := runBin(t, "-o", "nope"); r.code != 2 || !strings.Contains(r.stderr, "valid: python") {
		t.Fatalf("unknown id: %d %q", r.code, r.stderr)
	}
	if r := runBin(t, "-r", "/"); r.code != 2 || !strings.Contains(r.stderr, "refusing to scan") {
		t.Fatalf("root /: %d %q", r.code, r.stderr)
	}
	if r := runBin(t, "-a", "0"); r.code != 2 {
		t.Fatalf("age 0: %d", r.code)
	}
}

// AC-L2: golden output for help, man page, list and plain dry run.
func TestAC_L2_Golden(t *testing.T) {
	golden(t, "help", runBin(t, "--help").stdout)
	golden(t, "man", runBin(t, "--print-man").stdout)

	cfgFile := filepath.Join(t.TempDir(), "config.toml")
	testfix.Write(t, cfgFile, string(config.Defaults()))
	list := runBin(t, "-l", "-c", cfgFile, "-u", "cursor")
	if list.code != 0 {
		t.Fatalf("list: %d %s", list.code, list.stderr)
	}
	out := strings.ReplaceAll(list.stdout, cfgFile, "$CONFIG")
	if i := strings.Index(out, "Git: "); i >= 0 {
		out = out[:i] + "Git: $GIT\n"
	}
	golden(t, "list", out)

	root := e2eRoot(t)
	testfix.Write(t, filepath.Join(root, "rs/Cargo.toml"), "[package]\n")
	testfix.Write(t, filepath.Join(root, "rs/target/debug/app"), "12345")
	testfix.Write(t, filepath.Join(root, "py/__pycache__/m.pyc"), "1234567890")
	testfix.Write(t, filepath.Join(root, "notrust/target/important.db"), "data")
	r3 := testfix.ScenarioR3(t, root)
	_ = r3
	dry := runBin(t, "-T", "-s", "-r", root)
	if dry.code != 0 {
		t.Fatalf("dry run: %d %s", dry.code, dry.stderr)
	}
	golden(t, "dryrun", strings.ReplaceAll(dry.stdout, root, "$ROOT"))
}

// End-to-end AC-R1–R4 with --delete --force, plus the run log (AC-D3).
func TestReviewScenariosDeleteForce(t *testing.T) {
	root := e2eRoot(t)
	r1 := testfix.ScenarioR1(t, root)
	r3 := testfix.ScenarioR3(t, root)
	r4 := testfix.ScenarioR4(t, root)
	r5 := testfix.ScenarioR5(t, root)
	clone := testfix.FullClone(t, root, testfix.CloneOpts{LocalCommit: true})
	testfix.Write(t, filepath.Join(root, "rs/Cargo.toml"), "[package]\n")
	testfix.Write(t, filepath.Join(root, "rs/target/debug/app"), "12345")

	r := runBin(t, "-T", "-d", "-f", "-u", "orca,cursor", "-r", root)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s\n%s", r.code, r.stdout, r.stderr)
	}
	for _, keep := range []string{r1.Orca, r1.Fresh, r1.Locked, r3.TrackedBuild, r4.FreshRules, r4.FreshDir, r4.CommittedRules, r5, clone} {
		if !testfix.Exists(keep) {
			t.Errorf("deleted %s\n%s", keep, r.stdout)
		}
	}
	for _, gone := range []string{r3.Dist, filepath.Join(root, "rs/target")} {
		if testfix.Exists(gone) {
			t.Errorf("not deleted: %s\n%s", gone, r.stdout)
		}
	}
	data, err := os.ReadFile(filepath.Join(r.state, "cleanup", "runs.jsonl"))
	if err != nil || !bytes.Contains(data, []byte(`"type":"start"`)) || !bytes.Contains(data, []byte(`"type":"end"`)) {
		t.Fatalf("run log: %v\n%s", err, data)
	}
	if !strings.Contains(r.stdout, "Deleted 2 items") {
		t.Fatalf("report:\n%s", r.stdout)
	}
}

// AC-R5 and AC-F3 through the binary.
func TestConfigErrorsAndRoundTrip(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.toml")
	testfix.Write(t, bad, testfix.R5Config)
	r := runBin(t, "-l", "-c", bad)
	if r.code != 2 || !strings.Contains(r.stderr, bad+":9:") || !strings.Contains(r.stderr, "did you mean require_sibling_markers?") {
		t.Fatalf("R5: %d %q", r.code, r.stderr)
	}
	printed := runBin(t, "-P")
	f := filepath.Join(t.TempDir(), "c.toml")
	testfix.Write(t, f, printed.stdout)
	if r := runBin(t, "-c", f, "-l"); r.code != 0 {
		t.Fatalf("round trip: %d %s", r.code, r.stderr)
	}
}
