package main

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
)

func TestAC_V1_ResolveVersion(t *testing.T) {
	mk := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	tests := []struct {
		name, ldflag string
		bi           *debug.BuildInfo
		ok           bool
		want         string
	}{
		{"ldflag wins", "v1.2.3", mk("v0.0.1"), true, "v1.2.3"},
		{"devel", "", mk("(devel)"), true, "dev"},
		{"pseudo", "", mk("v0.0.0-20261121001240-9f1cf8802a37"), true, "dev"},
		{"tagged", "", mk("v0.0.1"), true, "v0.0.1"},
		{"dirty", "", mk("v0.0.1+dirty"), true, "v0.0.1+dirty"},
		{"no buildinfo", "", nil, false, "dev"},
		{"not ok", "", mk("v0.0.1"), false, "dev"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVersion(tc.ldflag, tc.bi, tc.ok); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestAC_V1_BinaryVersion(t *testing.T) {
	if got := runBin(t, "--version"); strings.TrimSpace(got.stdout) != "cleanup dev" {
		t.Fatalf("test binary: %q", got.stdout)
	}
	bin := filepath.Join(t.TempDir(), "cleanup")
	if out, err := exec.Command("go", "build", "-ldflags", "-X main.version=v0.0.1", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "cleanup v0.0.1" {
		t.Fatalf("got %q", out)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAC_I1_GoInstall(t *testing.T) {
	root := repoRoot(t)
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^module github\.com/jfricker/cleanup\s*$`).Match(mod) {
		t.Fatal("go.mod must declare module github.com/jfricker/cleanup")
	}
	gobin := t.TempDir()
	cmd := exec.Command("go", "install", "./cmd/cleanup")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOBIN="+gobin, "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go install: %v\n%s", err, out)
	}
	if out, err := exec.Command(filepath.Join(gobin, "cleanup"), "--version").CombinedOutput(); err != nil {
		t.Fatalf("--version: %v\n%s", err, out)
	}
}

func TestAC_I2_NoCgo(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == ".e2e-tmp" || n == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == `"C"` {
				t.Errorf("%s imports \"C\"", p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAC_I3_NoInstallTargets(t *testing.T) {
	mk, err := os.ReadFile(filepath.Join(repoRoot(t), "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^(install|uninstall|release)\s*:`)
	if m := re.Find(mk); m != nil {
		t.Fatalf("Makefile must not define %q", m)
	}
}
