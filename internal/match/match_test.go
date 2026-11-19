package match

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"cleanup/internal/config"
	"cleanup/internal/testfix"
	"cleanup/internal/walk"
)

func defaults(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadBytes("", nil)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func scan(t *testing.T, root string, cfg *config.Config, opts OptIns, retired []string, includeDisabled bool) []string {
	t.Helper()
	var vs []walk.Visitor
	for _, c := range cfg.Categories {
		if c.Enabled || includeDisabled {
			vs = append(vs, NewCategory(c, opts))
		}
	}
	if rv := NewRetired(cfg.Retired, retired); rv != nil {
		vs = append(vs, rv)
	}
	res, err := walk.Run(context.Background(), root, 0, vs, walk.Options{
		NeverTouch: cfg.NeverTouch, AncestorMarkers: AncestorMarkers(cfg.Categories)})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range res.Found {
		c := f.Payload.(Candidate)
		got = append(got, c.Cleaner+"/"+c.Rule+":"+c.Rel)
	}
	slices.Sort(got)
	return got
}

func TestBuiltInRuleMatrix(t *testing.T) {
	root := testfix.Root(t)
	for _, p := range []string{
		"py/__pycache__/m.pyc", "py/pkg.egg-info/PKG-INFO", "py/.coverage",
		"py/venv/pyvenv.cfg", "py/.venv/bin/activate", "py/notvenv-x/readme",
		"py/venv-nomarker/readme",
		"web/package.json", "web/.next/x", "web/src/dist/x", "web/a.tsbuildinfo",
		"web/node_modules/dep/dist/x", "web/test-results/r", "web/.eslintcache",
		"nopkg/dist/x", "nopkg/test-results/r",
		"rs/Cargo.toml", "rs/target/debug/x", "notrust/target/important.db",
		"sw/Package.swift", "sw/.build/x", "sw/DerivedData/x", "nosw/.build/x",
		"gr/build.gradle.kts", "gr/build/x", "gr/.gradle/x",
		"cm/build/CMakeCache.txt", "cm/cmake-build-debug/CMakeCache.txt", "cm/other/build/x",
		"mac/.DS_Store", "mac/._junk",
	} {
		testfix.Write(t, filepath.Join(root, p), "x")
	}
	cfg := defaults(t)
	got := scan(t, root, cfg, OptIns{}, nil, false)
	want := []string{
		"cmake/cmake-build:cm/build",
		"cmake/cmake-build:cm/cmake-build-debug",
		"gradle/gradle-build:gr/build",
		"gradle/gradle-cache:gr/.gradle",
		"node/artifact-files:web/.eslintcache",
		"node/artifact-files:web/a.tsbuildinfo",
		"node/framework-artifacts:web/.next",
		"node/generic-build-dirs:web/src/dist",
		"python/caches:py/__pycache__",
		"python/caches:py/pkg.egg-info",
		"python/coverage-file:py/.coverage",
		"rust/cargo-target:rs/target",
		"swift/derived-data:sw/DerivedData",
		"swift/swiftpm-build:sw/.build",
		"test-artifacts/js-test-reports:web/test-results",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}

	opt := scan(t, root, cfg, OptIns{Venvs: true, Modules: true}, nil, true)
	for _, w := range []string{"python/venvs:py/venv", "python/venvs:py/.venv", "node/node-modules:web/node_modules", "macos/finder-junk:mac/.DS_Store", "macos/finder-junk:mac/._junk"} {
		if !slices.Contains(opt, w) {
			t.Errorf("opt-in scan missing %s", w)
		}
	}
	for _, w := range []string{"python/venvs:py/venv-nomarker", "node/generic-build-dirs:web/node_modules/dep/dist"} {
		if slices.Contains(opt, w) {
			t.Errorf("opt-in scan should not contain %s", w)
		}
	}
}

// AC-R4 (matcher side): retired-tool paths are candidates only for retired tools.
func TestRetiredOnlyWhenListed(t *testing.T) {
	root := testfix.Root(t)
	testfix.ScenarioR4(t, root)
	cfg := defaults(t)
	if got := scan(t, root, cfg, OptIns{}, nil, false); slices.ContainsFunc(got, func(s string) bool { return s[:8] == "retired/" }) {
		t.Fatalf("retired candidates without --retired: %v", got)
	}
	got := scan(t, root, cfg, OptIns{}, []string{"cursor"}, false)
	want := []string{"retired/cursor:scratchpad/.cursor", "retired/cursor:scratchpad/.cursorrules", "retired/cursor:team/.cursorrules"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRetiredCandidateCarriesAgeAndGitTree(t *testing.T) {
	root := testfix.Root(t)
	r4 := testfix.ScenarioR4(t, root)
	cfg := defaults(t)
	rv := NewRetired(cfg.Retired, []string{"cursor"})
	res, err := walk.Run(context.Background(), root, 0, []walk.Visitor{rv}, walk.Options{NeverTouch: cfg.NeverTouch})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Found {
		c := f.Payload.(Candidate)
		if c.AgeDays != 30 {
			t.Fatalf("%s age gate %d", c.Rel, c.AgeDays)
		}
		if c.Path == r4.CommittedRules && c.GitTree != filepath.Dir(r4.CommittedRules) {
			t.Fatalf("committed rules git tree %q", c.GitTree)
		}
	}
}
