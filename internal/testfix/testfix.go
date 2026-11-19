// Package testfix builds filesystem and git fixtures for tests. Every git command
// runs with a scrubbed environment and a temporary HOME, so the developer's own git
// configuration never affects results.
package testfix

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Day is one day, for readability in age arithmetic.
const Day = 24 * time.Hour

// OldDays is the default age, in days, of fixture commits and files.
const OldDays = 90

// Env returns a minimal environment for running git in fixtures: no GIT_* variables
// from the parent, a private HOME, no system config and a fixed identity.
func Env(t testing.TB) []string {
	t.Helper()
	home := homeDir(t)
	env := []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(home, ".gitconfig"),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Fixture",
		"GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Fixture",
		"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	}
	return env
}

var (
	homesMu sync.Mutex
	homes   = map[testing.TB]string{}
)

func homeDir(t testing.TB) string {
	t.Helper()
	homesMu.Lock()
	defer homesMu.Unlock()
	if h, ok := homes[t]; ok {
		return h
	}
	h := t.TempDir()
	homes[t] = h
	t.Cleanup(func() {
		homesMu.Lock()
		delete(homes, t)
		homesMu.Unlock()
	})
	return h
}

// Git runs git in dir and returns trimmed stdout. It fails the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	return GitAt(t, dir, OldDays, args...)
}

// GitAt runs git in dir with author and committer dates set daysAgo days in the past.
func GitAt(t testing.TB, dir string, daysAgo int, args ...string) string {
	t.Helper()
	out, err := GitTry(dir, append(Env(t), dateEnv(daysAgo)...), args...)
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(out)
}

// GitTry runs git with the given environment and returns combined output and error.
func GitTry(dir string, env []string, args ...string) (string, error) {
	full := append([]string{"-c", "init.defaultBranch=main", "-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func dateEnv(daysAgo int) []string {
	d := time.Now().Add(-time.Duration(daysAgo) * Day).Format(time.RFC3339)
	return []string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}
}

// Write creates path (and parent directories) with content.
func Write(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Mkdir creates a directory and its parents.
func Mkdir(t testing.TB, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// Backdate sets the access and modification times of path to daysAgo days ago,
// without following symlinks into their targets.
func Backdate(t testing.TB, path string, daysAgo int) {
	t.Helper()
	ts := time.Now().Add(-time.Duration(daysAgo) * Day)
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatal(err)
	}
}

// BackdateTree backdates path and everything under it (symlinks are skipped).
func BackdateTree(t testing.TB, path string, daysAgo int) {
	t.Helper()
	var dirs []string
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, p) // directories last, so file writes don't bump them
			return nil
		}
		Backdate(t, p, daysAgo)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		Backdate(t, dirs[i], daysAgo)
	}
}

// Exists reports whether path exists (without following a final symlink).
func Exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Canonical returns the symlink-resolved absolute form of path (macOS /var →
// /private/var), failing the test on error.
func Canonical(t testing.TB, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Root returns a fresh canonical temporary directory to use as a scan root.
func Root(t testing.TB) string {
	t.Helper()
	return Canonical(t, t.TempDir())
}

// Repo initialises a repository at dir with one old commit (a README) and returns
// dir. When ignore is non-empty it is committed as .gitignore.
func Repo(t testing.TB, dir string, ignore ...string) string {
	t.Helper()
	Mkdir(t, dir)
	Git(t, dir, "init", "-q")
	Write(t, filepath.Join(dir, "README.md"), "fixture\n")
	if len(ignore) > 0 {
		Write(t, filepath.Join(dir, ".gitignore"), strings.Join(ignore, "\n")+"\n")
	}
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// Commit writes name with content in dir and commits it daysAgo days in the past.
func Commit(t testing.TB, dir, name, content string, daysAgo int) {
	t.Helper()
	Write(t, filepath.Join(dir, name), content)
	GitAt(t, dir, daysAgo, "add", "--", name)
	GitAt(t, dir, daysAgo, "commit", "-q", "-m", "add "+name)
}

// Clone makes a full clone of src at dst (src becomes its origin).
func Clone(t testing.TB, src, dst string) string {
	t.Helper()
	Git(t, filepath.Dir(dst), "clone", "-q", src, dst)
	return dst
}

// Worktree adds a linked worktree of repo at path on a new branch.
func Worktree(t testing.TB, repo, path, branch string) string {
	t.Helper()
	Git(t, repo, "worktree", "add", "-q", "-b", branch, path)
	return path
}

// Name returns a unique-ish suffix for marker files in hostile-config tests.
func Name(t testing.TB) string {
	return fmt.Sprintf("pwned-%d", time.Now().UnixNano())
}
