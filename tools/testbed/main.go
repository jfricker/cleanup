// Command testbed builds a disposable directory of projects that exercises every
// state cleanup can report: eligible artifacts, opt-in virtualenvs and node_modules,
// marker-less look-alikes, tracked build output, worktrees that are stale, fresh,
// locked, dirty, unpushed or gone, retired-tool leftovers (old, fresh, committed),
// a protected path, a symlink and a skipped git tree inside a retired tool.
//
//	go run ./tools/testbed -out DIR
//
// DIR must not exist or be empty. It gets DIR/Projects (the scan root),
// DIR/elsewhere (upstreams and moved-away worktrees, outside the root) and
// DIR/config.toml (marks tools retired and protects one path). Nothing is ever
// deleted: the command refuses a non-empty DIR instead of clearing it.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const day = 24 * time.Hour

type bed struct {
	out, root, elsewhere, home string
	env                        []string
}

func main() {
	out := flag.String("out", "", "directory to create the test bed in (must not exist or be empty)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: testbed -out DIR")
		os.Exit(2)
	}
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "testbed:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	out, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if ents, err := os.ReadDir(out); err == nil && len(ents) > 0 {
		return fmt.Errorf("%s is not empty; pick a new directory (testbed never deletes)", out)
	}
	// Resolve symlinks (macOS /var -> /private/var) so paths match the scanner's.
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if out, err = filepath.EvalSymlinks(out); err != nil {
		return err
	}
	b := &bed{out: out, root: filepath.Join(out, "Projects"), elsewhere: filepath.Join(out, "elsewhere"), home: filepath.Join(out, "elsewhere", "home")}
	b.env = []string{
		"HOME=" + b.home, "PATH=" + os.Getenv("PATH"), "LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + filepath.Join(b.home, ".gitconfig"),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Testbed", "GIT_AUTHOR_EMAIL=testbed@example.invalid",
		"GIT_COMMITTER_NAME=Testbed", "GIT_COMMITTER_EMAIL=testbed@example.invalid",
	}
	b.mkdir(b.home)

	steps := []func() error{
		b.python, b.node, b.otherLanguages, b.lookalikes,
		b.worktrees, b.retired, b.protectedAndSymlink, b.config,
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return err
		}
	}
	fmt.Printf("test bed ready\n  root:   %s\n  config: %s\n\nrun:\n  ./cleanup -c %s -d -v -m\n",
		b.root, filepath.Join(out, "config.toml"), filepath.Join(out, "config.toml"))
	return nil
}

// --- projects ---------------------------------------------------------------

// python: a stale venv in an idle, pushed project and a fresh venv in an active one.
// cleanup does not age-gate venvs today, so both are eligible with -v.
func (b *bed) python() error {
	stale := b.pushedRepo("py-api-legacy", 200, ".venv/", "__pycache__/", ".pytest_cache/", ".mypy_cache/")
	b.commit(stale, "pyproject.toml", "[project]\nname = \"api\"\n", 200)
	b.venv(filepath.Join(stale, ".venv"), 6<<20)
	b.blob(filepath.Join(stale, "api", "__pycache__", "app.cpython-312.pyc"), 64<<10)
	b.blob(filepath.Join(stale, ".pytest_cache", "v", "cache", "nodeids"), 8<<10)
	b.blob(filepath.Join(stale, ".mypy_cache", "3.12", "app.data.json"), 400<<10)
	b.backdate(stale, 200)

	active := b.pushedRepo("py-ml-active", 2, ".venv/", "__pycache__/")
	b.commit(active, "pyproject.toml", "[project]\nname = \"ml\"\n", 2)
	b.venv(filepath.Join(active, ".venv"), 9<<20)
	b.blob(filepath.Join(active, "ml", "__pycache__", "train.cpython-312.pyc"), 96<<10)
	b.backdate(active, 0)

	// A plain-directory project (no git) with an old venv named venv-3.11.
	scripts := filepath.Join(b.root, "scratch-scripts")
	b.venv(filepath.Join(scripts, "venv-3.11"), 3<<20)
	b.write(filepath.Join(scripts, "scrape.py"), "print('hi')\n")
	b.backdate(scripts, 400)
	return nil
}

// node: an unused app with old node_modules and every artifact kind, an active app
// with fresh node_modules, and a repo whose build/ is committed (tracked, skipped).
func (b *bed) node() error {
	old := b.pushedRepo("web-dashboard-2023", 300, "node_modules/", ".next/", "dist/", "coverage/",
		".eslintcache", "*.tsbuildinfo", "playwright-report/", "test-results/", ".turbo/")
	b.commit(old, "package.json", `{"name":"dashboard"}`+"\n", 300)
	b.nodeModules(filepath.Join(old, "node_modules"), 40, 300<<10)
	b.blob(filepath.Join(old, ".next", "cache", "webpack", "client.pack"), 5<<20)
	b.blob(filepath.Join(old, "dist", "main.js"), 700<<10)
	b.blob(filepath.Join(old, "coverage", "lcov.info"), 200<<10)
	b.blob(filepath.Join(old, ".eslintcache"), 40<<10)
	b.blob(filepath.Join(old, "tsconfig.tsbuildinfo"), 120<<10)
	b.blob(filepath.Join(old, "playwright-report", "index.html"), 300<<10)
	b.blob(filepath.Join(old, "test-results", "run-1", "trace.zip"), 900<<10)
	b.blob(filepath.Join(old, ".turbo", "cache", "a.tar.zst"), 1<<20)
	b.backdate(old, 300)

	active := b.pushedRepo("web-shop", 1, "node_modules/", ".vite/")
	b.commit(active, "package.json", `{"name":"shop"}`+"\n", 1)
	b.nodeModules(filepath.Join(active, "node_modules"), 60, 250<<10)
	b.blob(filepath.Join(active, ".vite", "deps", "chunk.js"), 300<<10)
	b.backdate(active, 0)

	tracked := b.pushedRepo("web-tracked-build", 90, "dist/")
	b.commit(tracked, "package.json", "{}\n", 90)
	b.commit(tracked, "build/release.sh", "#!/bin/sh\n", 90)
	b.blob(filepath.Join(tracked, "dist", "bundle.js"), 500<<10)
	b.backdate(tracked, 90)

	// An abandoned monorepo with nested workspaces: node_modules inside node_modules
	// is pruned, nested packages each have their own.
	mono := b.pushedRepo("monorepo-abandoned", 500, "node_modules/", "dist/")
	b.commit(mono, "package.json", `{"workspaces":["packages/*"]}`+"\n", 500)
	b.nodeModules(filepath.Join(mono, "node_modules"), 30, 200<<10)
	for _, p := range []string{"ui", "api", "shared"} {
		b.commit(mono, "packages/"+p+"/package.json", "{}\n", 500)
		b.nodeModules(filepath.Join(mono, "packages", p, "node_modules"), 8, 150<<10)
		b.blob(filepath.Join(mono, "packages", p, "dist", "index.js"), 150<<10)
	}
	b.backdate(mono, 500)
	return nil
}

func (b *bed) otherLanguages() error {
	rs := b.pushedRepo("rust-cli", 120, "target/")
	b.commit(rs, "Cargo.toml", "[package]\nname = \"cli\"\n", 120)
	b.blob(filepath.Join(rs, "target", "debug", "cli"), 8<<20)
	b.blob(filepath.Join(rs, "target", "debug", "deps", "libserde.rlib"), 4<<20)
	b.backdate(rs, 120)

	sw := b.pushedRepo("swift-kit", 60, ".build/", ".swiftpm/")
	b.commit(sw, "Package.swift", "// swift-tools-version:5.9\n", 60)
	b.blob(filepath.Join(sw, ".build", "debug", "Kit.swiftmodule"), 3<<20)
	b.blob(filepath.Join(sw, ".swiftpm", "xcode", "package.xcworkspace"), 16<<10)
	b.blob(filepath.Join(sw, "DerivedData", "Index", "store"), 2<<20)
	b.backdate(sw, 60)

	gr := b.pushedRepo("android-app", 150, ".gradle/", "build/")
	b.commit(gr, "settings.gradle.kts", "rootProject.name = \"app\"\n", 150)
	b.commit(gr, "build.gradle.kts", "plugins {}\n", 150)
	b.blob(filepath.Join(gr, ".gradle", "8.5", "fileHashes", "fileHashes.bin"), 1<<20)
	b.blob(filepath.Join(gr, "build", "outputs", "apk", "app-debug.apk"), 6<<20)
	b.backdate(gr, 150)

	cm := b.pushedRepo("cpp-engine", 80, "cmake-build-debug/", "build/")
	b.commit(cm, "CMakeLists.txt", "project(engine)\n", 80)
	b.write(filepath.Join(cm, "cmake-build-debug", "CMakeCache.txt"), "CMAKE_BUILD_TYPE:STRING=Debug\n")
	b.blob(filepath.Join(cm, "cmake-build-debug", "engine"), 4<<20)
	b.write(filepath.Join(cm, "build", "CMakeCache.txt"), "CMAKE_BUILD_TYPE:STRING=Release\n")
	b.blob(filepath.Join(cm, "build", "engine"), 3<<20)
	b.backdate(cm, 80)

	// macOS Finder junk: the macos cleaner is off by default (-o macos to see it).
	b.write(filepath.Join(b.root, "cpp-engine", ".DS_Store"), "x")
	return nil
}

// lookalikes: directories that share an artifact's name but lack its marker, so
// nothing is proposed for them.
func (b *bed) lookalikes() error {
	b.blob(filepath.Join(b.root, "not-rust", "target", "important.db"), 1<<20)    // no Cargo.toml
	b.blob(filepath.Join(b.root, "not-a-venv", "venv", "notes.txt"), 64<<10)      // no pyvenv.cfg
	b.blob(filepath.Join(b.root, "photos-export", "build", "img-001.jpg"), 2<<20) // no package.json/gradle/cmake
	b.backdate(filepath.Join(b.root, "not-rust"), 365)
	b.backdate(filepath.Join(b.root, "not-a-venv"), 365)
	b.backdate(filepath.Join(b.root, "photos-export"), 365)
	return nil
}

// worktrees: one repo with linked worktrees in every state.
func (b *bed) worktrees() error {
	repo := b.pushedRepo("service", 45)
	b.commit(repo, "main.go", "package main\n", 45)
	add := func(name, branch string) string {
		wt := filepath.Join(b.root, "service-wt", name)
		b.git(repo, 45, "worktree", "add", "-q", "--track", "-b", branch, wt, "origin/main")
		return wt
	}
	stale := add("stale-feature", "stale-feature") // clean, merged, 70 days idle
	b.backdate(stale, 70)

	fresh := add("fresh-feature", "fresh-feature") // clean but touched 3 days ago
	b.backdate(fresh, 3)

	locked := add("locked-agent", "agent-run") // locked by an agent
	b.git(repo, 45, "worktree", "lock", "--reason", "claude agent running", locked)
	b.backdate(locked, 50)

	dirty := add("dirty-spike", "spike") // uncommitted edits
	b.write(filepath.Join(dirty, "main.go"), "package main // wip\n")
	b.backdate(dirty, 60)

	untracked := add("untracked-notes", "notes") // untracked file
	b.write(filepath.Join(untracked, "TODO.txt"), "remember\n")
	b.backdate(untracked, 60)

	unpushed := add("unpushed-fix", "fix") // one commit on no remote: shown with a note
	b.localCommit(unpushed, "fix.go", "package main\n", 55)
	b.backdate(unpushed, 55)

	// A worktree whose directory was moved away: git still lists it ("gone" entry).
	gone := add("moved-away", "moved")
	b.backdate(gone, 80)
	if err := os.Rename(gone, filepath.Join(b.elsewhere, "moved-away")); err != nil {
		return err
	}

	// A worktree outside the scan root, registered by a repo inside it.
	outside := filepath.Join(b.elsewhere, "outside-wt")
	b.git(repo, 45, "worktree", "add", "-q", "--track", "-b", "outside", outside, "origin/main")
	b.backdate(outside, 90)

	b.backdate(repo, 45)
	return nil
}

// retired: leftovers of tools marked retired in config.toml.
func (b *bed) retired() error {
	old := filepath.Join(b.root, "old-experiment") // plain dir, 120 days idle
	b.write(filepath.Join(old, ".cursorrules"), "be terse\n")
	b.blob(filepath.Join(old, ".cursor", "rules", "style.mdc"), 4<<10)
	b.blob(filepath.Join(old, ".aider.chat.history.md"), 600<<10)
	b.blob(filepath.Join(old, ".aider.tags.cache.v3", "cache.db"), 2<<20)
	b.write(filepath.Join(old, ".aider.input.history"), "/add main.py\n")
	b.blob(filepath.Join(old, ".windsurf", "memories.json"), 32<<10)
	b.write(filepath.Join(old, "main.py"), "print(1)\n")
	b.backdate(old, 120)

	fresh := filepath.Join(b.root, "new-idea") // touched today: age-gated
	b.write(filepath.Join(fresh, ".cursorrules"), "rules\n")
	b.blob(filepath.Join(fresh, ".zed", "settings.json"), 2<<10)

	team := b.pushedRepo("team-handbook", 60) // committed .cursorrules: tracked, never deleted
	b.commit(team, ".cursorrules", "team rules\n", 60)
	b.commit(team, "README.md", "# handbook\n", 60)
	b.backdate(team, 60)

	// A retired agent workspace that contains a locked worktree: the whole .orca is
	// skipped because it holds a skipped git tree.
	host := b.pushedRepo("agent-host", 40)
	orca := filepath.Join(b.root, ".orca")
	b.write(filepath.Join(orca, "state.json"), "{}\n")
	b.blob(filepath.Join(orca, "logs", "run.log"), 1<<20)
	ws := filepath.Join(orca, "workspaces", "ws1")
	b.git(host, 40, "worktree", "add", "-q", "--track", "-b", "ws1", ws, "origin/main")
	b.git(host, 40, "worktree", "lock", "--reason", "orca session", ws)
	b.backdate(orca, 90)
	b.backdate(host, 40)
	return nil
}

func (b *bed) protectedAndSymlink() error {
	// protected_paths in config.toml: never walked, proposed or deleted.
	cw := filepath.Join(b.root, "client-work")
	b.write(filepath.Join(cw, "package.json"), "{}\n")
	b.nodeModules(filepath.Join(cw, "node_modules"), 10, 200<<10)
	b.blob(filepath.Join(cw, "dist", "app.js"), 300<<10)
	b.backdate(cw, 200)

	// A symlinked node_modules pointing outside the root is never followed.
	link := filepath.Join(b.root, "linked-deps")
	b.write(filepath.Join(link, "package.json"), "{}\n")
	target := filepath.Join(b.elsewhere, "shared-node-modules")
	b.blob(filepath.Join(target, "left-pad", "index.js"), 8<<10)
	if err := os.Symlink(target, filepath.Join(link, "node_modules")); err != nil {
		return err
	}
	b.backdate(link, 100)
	return nil
}

func (b *bed) config() error {
	cfg := fmt.Sprintf(`version = 1

project_root = %q
protected_paths = ["client-work"]

[[retired_tools.tools]]
id = "cursor"
retired = true

[[retired_tools.tools]]
id = "aider"
retired = true

[[retired_tools.tools]]
id = "zed"
retired = true

[[retired_tools.tools]]
id = "orca"
retired = true
`, b.root)
	b.write(filepath.Join(b.out, "config.toml"), cfg)
	return nil
}

// --- helpers ----------------------------------------------------------------

// pushedRepo creates root/name as a clone of an upstream under elsewhere/, so its
// commits are on a remote, with the given .gitignore lines committed.
func (b *bed) pushedRepo(name string, daysAgo int, ignore ...string) string {
	up := filepath.Join(b.elsewhere, "upstreams", name+".git")
	seed := filepath.Join(b.elsewhere, "seeds", name)
	b.mkdir(seed)
	b.git(seed, daysAgo, "init", "-q")
	gi := strings.Join(ignore, "\n")
	if gi != "" {
		gi += "\n"
	}
	b.write(filepath.Join(seed, ".gitignore"), gi)
	b.write(filepath.Join(seed, "README.md"), "# "+name+"\n")
	b.git(seed, daysAgo, "add", "-A")
	b.git(seed, daysAgo, "commit", "-q", "-m", "init")
	b.git(b.elsewhere, daysAgo, "clone", "-q", "--bare", seed, up)
	dir := filepath.Join(b.root, name)
	b.git(b.elsewhere, daysAgo, "clone", "-q", up, dir)
	return dir
}

// commit writes and commits a file, then pushes so the commit is on the remote.
func (b *bed) commit(repo, rel, content string, daysAgo int) {
	b.write(filepath.Join(repo, rel), content)
	b.git(repo, daysAgo, "add", "--", rel)
	b.git(repo, daysAgo, "commit", "-q", "-m", "add "+rel)
	b.git(repo, daysAgo, "push", "-q")
}

// localCommit writes and commits a file without pushing it.
func (b *bed) localCommit(repo, rel, content string, daysAgo int) {
	b.write(filepath.Join(repo, rel), content)
	b.git(repo, daysAgo, "add", "--", rel)
	b.git(repo, daysAgo, "commit", "-q", "-m", "add "+rel)
}

func (b *bed) git(dir string, daysAgo int, args ...string) {
	d := time.Now().Add(-time.Duration(daysAgo) * day).Format(time.RFC3339)
	full := append([]string{"-c", "init.defaultBranch=main", "-c", "core.hooksPath=/dev/null", "-c", "push.default=current"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(b.env, "GIT_AUTHOR_DATE="+d, "GIT_COMMITTER_DATE="+d)
	if out, err := cmd.CombinedOutput(); err != nil {
		die("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

func (b *bed) venv(dir string, size int) {
	b.write(filepath.Join(dir, "pyvenv.cfg"), "home = /usr/bin\nversion = 3.12.4\n")
	b.write(filepath.Join(dir, "bin", "activate"), "# activate\n")
	per := size / 4
	for _, pkg := range []string{"numpy", "pandas", "requests", "urllib3"} {
		b.blob(filepath.Join(dir, "lib", "python3.12", "site-packages", pkg, "__init__.py"), per)
	}
}

func (b *bed) nodeModules(dir string, n, each int) {
	for i := range n {
		b.blob(filepath.Join(dir, fmt.Sprintf("pkg-%02d", i), "index.js"), each)
	}
	b.write(filepath.Join(dir, ".package-lock.json"), "{}\n")
}

func (b *bed) blob(path string, size int) {
	b.write(path, string(bytes.Repeat([]byte{'x'}, size)))
}

func (b *bed) write(path, content string) {
	b.mkdir(filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		die("%v", err)
	}
}

func (b *bed) mkdir(path string) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		die("%v", err)
	}
}

// backdate sets mtime/atime of path and everything under it (symlinks skipped),
// deepest first so directory times are not bumped afterwards.
func (b *bed) backdate(path string, daysAgo int) {
	ts := time.Now().Add(-time.Duration(daysAgo) * day)
	var dirs []string
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		return os.Chtimes(p, ts, ts)
	})
	if err != nil {
		die("%v", err)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chtimes(dirs[i], ts, ts); err != nil {
			die("%v", err)
		}
	}
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "testbed: "+format+"\n", args...)
	os.Exit(1)
}
