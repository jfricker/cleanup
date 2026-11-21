// Package rootguard canonicalises the scan root and refuses roots where a cleanup
// run could destroy something that is not a project tree (SPEC ROOT-1…5).
package rootguard

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// Match says how a refusal rule compares paths.
type Match int

const (
	// Exact refuses only the path itself.
	Exact Match = iota + 1
	// Subtree refuses the path and everything under it.
	Subtree
)

// Rule refuses a root.
type Rule struct {
	Path   string // canonical absolute path
	Match  Match
	Reason string
}

// Options adjust Resolve. The zero value applies the full guard with the real home
// directory; tests may substitute a home and the system rule set.
type Options struct {
	Home        string // canonical home; from the password database when empty
	SystemRules []Rule // nil selects the defaults for this OS
}

// Root is a validated scan root.
type Root struct {
	Path string   // canonical absolute path
	Dev  uint64   // device of the root, for staying on one filesystem
	FS   *os.Root // all mutation under the root goes through this handle
}

// Close releases the root handle.
func (r Root) Close() error { return r.FS.Close() }

// RefusedError reports a root that a rule refuses.
type RefusedError struct {
	Path   string
	Reason string
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("refusing to scan %s: %s", e.Path, e.Reason)
}

// Resolve canonicalises path, applies the refusal rules and opens the root.
func Resolve(path string, o Options) (Root, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Root{}, fmt.Errorf("root: %w", err)
	}
	canon, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Root{}, fmt.Errorf("root: %w", err)
	}
	fi, err := os.Lstat(canon)
	if err != nil {
		return Root{}, fmt.Errorf("root: %w", err)
	}
	if !fi.IsDir() {
		return Root{}, fmt.Errorf("root: %s is not a directory", canon)
	}

	home := o.Home
	if home == "" {
		if home, err = homeDir(); err != nil {
			return Root{}, err
		}
	}
	rules := o.SystemRules
	if rules == nil {
		rules = SystemRules(runtime.GOOS)
	}
	rules = append(append([]Rule(nil), rules...), HomeRules(home)...)
	if err := Check(canon, rules); err != nil {
		return Root{}, err
	}

	fsRoot, err := os.OpenRoot(canon)
	if err != nil {
		return Root{}, fmt.Errorf("root: %w", err)
	}
	return Root{Path: canon, Dev: devOf(fi), FS: fsRoot}, nil
}

// Check returns a *RefusedError if any rule refuses canon.
func Check(canon string, rules []Rule) error {
	for _, r := range rules {
		switch r.Match {
		case Exact:
			if canon == r.Path {
				return &RefusedError{Path: canon, Reason: r.Reason}
			}
		case Subtree:
			if canon == r.Path || strings.HasPrefix(canon, strings.TrimSuffix(r.Path, "/")+"/") {
				return &RefusedError{Path: canon, Reason: r.Reason}
			}
		}
	}
	return nil
}

// Home returns the canonical (symlink-resolved) home directory from the password
// database (SPEC ROOT-2).
func Home() (string, error) { return homeDir() }

// Expand replaces a leading "~" or "~/" in p with home. Other forms, including
// "~user/x", are returned unchanged.
func Expand(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func homeDir() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("root: cannot determine home directory: %w", err)
	}
	h, err := filepath.EvalSymlinks(u.HomeDir)
	if err != nil {
		return "", fmt.Errorf("root: home directory: %w", err)
	}
	return h, nil
}

// HomeRules refuses home, every ancestor of home, the personal document folders and
// the Library tree (which holds iCloud Drive and cloud-sync folders).
func HomeRules(home string) []Rule {
	rules := []Rule{
		{Path: "/", Match: Exact, Reason: "filesystem root"},
		{Path: home, Match: Exact, Reason: "home directory"},
	}
	for p := filepath.Dir(home); p != "/" && p != "."; p = filepath.Dir(p) {
		rules = append(rules, Rule{Path: p, Match: Exact, Reason: "ancestor of the home directory"})
	}
	for _, d := range []string{"Documents", "Desktop", "Downloads"} {
		rules = append(rules, Rule{Path: filepath.Join(home, d), Match: Exact, Reason: "personal folder ~/" + d})
	}
	return append(rules,
		Rule{Path: filepath.Join(home, "Library"), Match: Subtree,
			Reason: "~/Library (includes iCloud Drive and ~/Library/CloudStorage)"},
	)
}

// SystemRules returns the system-folder refusals for goos.
func SystemRules(goos string) []Rule {
	var dirs []string
	switch goos {
	case "darwin":
		dirs = []string{"/System", "/Library", "/Applications", "/usr", "/bin", "/sbin",
			"/etc", "/var", "/private", "/opt/homebrew"}
	default:
		dirs = []string{"/proc", "/sys", "/dev", "/boot", "/usr", "/etc", "/var", "/bin", "/sbin",
			"/lib", "/lib32", "/lib64", "/libx32"}
	}
	rules := make([]Rule, len(dirs))
	for i, d := range dirs {
		rules[i] = Rule{Path: d, Match: Subtree, Reason: "system folder " + d}
	}
	return rules
}

func devOf(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev) // int32 on darwin, uint64 on linux
	}
	return 0
}
