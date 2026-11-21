package rootguard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfricker/cleanup/internal/testfix"
)

// AC-C3: every refused root is rejected; ordinary project folders are accepted.
func TestAC_C3_RefusedRoots(t *testing.T) {
	home := "/Users/someone"
	rules := append(SystemRules("darwin"), HomeRules(home)...)
	refused := []string{
		"/", "/Users", home,
		home + "/Documents", home + "/Desktop", home + "/Downloads",
		home + "/Library", home + "/Library/Mobile Documents/com~apple~CloudDocs",
		home + "/Library/CloudStorage/Dropbox",
		"/System", "/System/Library", "/Library/Caches", "/Applications", "/usr/local",
		"/bin", "/sbin", "/etc", "/var/log", "/private/tmp", "/opt/homebrew/Cellar",
	}
	for _, p := range refused {
		var re *RefusedError
		if err := Check(p, rules); !errors.As(err, &re) || re.Reason == "" {
			t.Errorf("%s: want refusal, got %v", p, err)
		}
	}
	allowed := []string{
		home + "/Projects", home + "/Documents/code", home + "/src/x",
		"/Volumes/External/Projects", "/opt/projects", "/Users/other",
	}
	for _, p := range allowed {
		if err := Check(p, rules); err != nil {
			t.Errorf("%s: unexpected refusal %v", p, err)
		}
	}

	linux := append(SystemRules("linux"), HomeRules("/home/someone")...)
	for _, p := range []string{"/proc/1", "/sys", "/dev", "/boot", "/lib64/x", "/home", "/home/someone"} {
		if err := Check(p, linux); err == nil {
			t.Errorf("linux %s: want refusal", p)
		}
	}
}

func TestResolveCanonicalisesAndOpens(t *testing.T) {
	real := testfix.Root(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	r, err := Resolve(link, Options{Home: "/nonexistent-home", SystemRules: []Rule{}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Path != real || r.Dev == 0 {
		t.Fatalf("root %+v, want path %s", r, real)
	}
	if _, err := r.FS.Lstat("."); err != nil {
		t.Fatal(err)
	}
}

func TestResolveRefusesHomeViaSymlink(t *testing.T) {
	home := testfix.Root(t)
	link := filepath.Join(t.TempDir(), "h")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(link, Options{Home: home, SystemRules: []Rule{}})
	if err == nil || !strings.Contains(err.Error(), "home directory") || !strings.Contains(err.Error(), home) {
		t.Fatalf("got %v", err)
	}
}

func TestResolveDefaultRefusesTempOnDarwin(t *testing.T) {
	if _, err := os.Stat("/private"); err != nil {
		t.Skip("not macOS")
	}
	if _, err := Resolve(t.TempDir(), Options{}); err == nil {
		t.Fatal("temp dir under /private must be refused by the default rules")
	}
}

func TestResolveRejectsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f")
	testfix.Write(t, f, "x")
	if _, err := Resolve(f, Options{SystemRules: []Rule{}, Home: "/x"}); err == nil {
		t.Fatal("want error for a file root")
	}
}

func TestExpand(t *testing.T) {
	const home = "/Users/someone"
	tests := []struct{ in, want string }{
		{"~", home},
		{"~/", home},
		{"~/Projects", home + "/Projects"},
		{"~/a/b", home + "/a/b"},
		{"~user/x", "~user/x"},
		{"/abs/path", "/abs/path"},
		{"rel/~/x", "rel/~/x"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := Expand(tc.in, home); got != tc.want {
			t.Errorf("Expand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHome_Absolute(t *testing.T) {
	h, err := Home()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(h) {
		t.Errorf("Home() = %q, want absolute path", h)
	}
}
