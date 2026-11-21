package scan

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/gitx"
	"github.com/jfricker/cleanup/internal/match"
	"github.com/jfricker/cleanup/internal/rootguard"
)

// BenchmarkScan measures a dry-run scan over the NFR-1 fixture: 300 repositories and
// 30 node_modules trees of 20,000 files (about 650k entries; CLEANUP_BENCH_SCALE
// scales it). The fixture is cached in the temp directory between runs. Run with
// `make bench`; it is not part of `make check`.
func BenchmarkScan(b *testing.B) {
	scale := 1.0
	if s := os.Getenv("CLEANUP_BENCH_SCALE"); s != "" {
		var err error
		if scale, err = strconv.ParseFloat(s, 64); err != nil {
			b.Fatal(err)
		}
	}
	dir := benchFixture(b, scale)
	root, err := rootguard.Resolve(dir, rootguard.Options{Home: "/nonexistent", SystemRules: []rootguard.Rule{}})
	if err != nil {
		b.Fatal(err)
	}
	defer root.Close()
	git, err := gitx.New(gitx.Options{})
	if err != nil {
		b.Fatal(err)
	}
	cfg, err := config.LoadBytes("", nil)
	if err != nil {
		b.Fatal(err)
	}

	start := time.Now()
	if out, err := exec.Command("find", dir, "-type", "d").Output(); err != nil || len(out) == 0 {
		b.Fatal(err)
	}
	findTime := time.Since(start)

	b.ResetTimer()
	var res *Result
	for range b.N {
		res, err = Run(context.Background(), cfg, root, git, Options{OptIns: match.OptIns{Modules: true, Venvs: true}}, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	per := b.Elapsed() / time.Duration(b.N)
	b.ReportMetric(per.Seconds(), "s/scan")
	b.ReportMetric(per.Seconds()/findTime.Seconds(), "x-find")
	b.Logf("dirs=%d verdicts=%d scan=%v find -type d=%v (NFR-1: <=15s and <=2x find)", res.Dirs, len(res.Verdicts), per, findTime)
}

func benchFixture(b *testing.B, scale float64) string {
	b.Helper()
	repos, trees, files := int(300*scale), int(30*scale), 20000
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("cleanup-bench-%d-%d", repos, trees))
	marker := filepath.Join(dir, ".complete")
	if _, err := os.Stat(marker); err == nil {
		return dir
	}
	b.Logf("building fixture in %s (one-time)", dir)
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	for r := range repos {
		repo := filepath.Join(dir, fmt.Sprintf("org%02d", r%20), fmt.Sprintf("repo%03d", r))
		write(filepath.Join(repo, "package.json"), "{}")
		write(filepath.Join(repo, "src", "index.ts"), "export {}")
		write(filepath.Join(repo, "dist", "index.js"), "x")
		write(filepath.Join(repo, "pkg", "__pycache__", "m.pyc"), "x")
		write(filepath.Join(repo, ".gitignore"), "dist/\nnode_modules/\n__pycache__/\n")
		cmd := exec.Command("git", "-c", "init.defaultBranch=main", "init", "-q")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("git init: %v %s", err, out)
		}
		if r < trees {
			for f := range files {
				write(filepath.Join(repo, "node_modules", fmt.Sprintf("pkg%03d", f/100), fmt.Sprintf("f%03d.js", f%100)), "module.exports = 1\n")
			}
		}
	}
	write(marker, "")
	return dir
}
