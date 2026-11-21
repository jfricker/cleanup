package scan

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jfricker/cleanup/internal/config"
	"github.com/jfricker/cleanup/internal/testfix"
)

func probeIDs(fps []ToolFootprint) []string {
	var ids []string
	for _, f := range fps {
		ids = append(ids, f.Tool.ID)
	}
	return ids
}

func TestAC_T1_ProbeListsFoundToolsNewestFirst(t *testing.T) {
	root, git, cfg := setup(t)
	testfix.Write(t, filepath.Join(root.Path, "proj/.cursor/rules/a.mdc"), "x")
	testfix.BackdateTree(t, filepath.Join(root.Path, "proj/.cursor"), 40)
	testfix.Write(t, filepath.Join(root.Path, "proj/.aider.chat.history.md"), "x")
	testfix.BackdateTree(t, filepath.Join(root.Path, "proj/.aider.chat.history.md"), 2)

	fps, err := Probe(context.Background(), cfg, root, git, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := probeIDs(fps); !reflect.DeepEqual(got, []string{"aider", "cursor"}) {
		t.Fatalf("tools = %v, want [aider cursor]", got)
	}
	if fps[0].TooRecent == 0 || fps[0].Items == 0 {
		t.Errorf("aider should be too recent: %+v", fps[0])
	}
	if fps[1].TooRecent != 0 || fps[1].Items == 0 || fps[1].Size == 0 {
		t.Errorf("cursor should be eligible only: %+v", fps[1])
	}
}

func TestAC_T2_ProbeSkipsProtectedAndNeverTouch(t *testing.T) {
	root, git, cfg := setup(t)
	testfix.Write(t, filepath.Join(root.Path, "protected/.cursor/a"), "x")
	testfix.Write(t, filepath.Join(root.Path, "a/.wrangler/sub/.windsurf/a"), "x")
	testfix.BackdateTree(t, root.Path, 60)
	fps, err := Probe(context.Background(), cfg, root, git, []string{"protected"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fps) != 0 {
		t.Fatalf("expected no footprints, got %v", probeIDs(fps))
	}
}

func TestAC_T3_ProbeAgeMatchesInspector(t *testing.T) {
	root, git, cfg := setup(t)
	testfix.Write(t, filepath.Join(root.Path, "loose/.cursor/rules/a.mdc"), "x")
	testfix.Write(t, filepath.Join(root.Path, "loose/.cursor/rules/b.mdc"), "x")
	testfix.BackdateTree(t, filepath.Join(root.Path, "loose/.cursor"), 40)
	testfix.Backdate(t, filepath.Join(root.Path, "loose/.cursor/rules/b.mdc"), 35)
	repo := testfix.Repo(t, filepath.Join(root.Path, "repo"))
	testfix.Write(t, filepath.Join(repo, ".aider.chat.history.md"), "x")
	testfix.Write(t, filepath.Join(repo, ".gitignore"), ".aider*\n")
	testfix.BackdateTree(t, repo, 50)
	now := time.Now
	fps, err := probe(context.Background(), cfg, root, git, nil, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), cfg, root, git, Options{Only: []string{config.RetiredID}, Retired: cfg.ToolIDs(), Now: now}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]time.Duration{}
	for _, v := range res.Verdicts {
		if d, ok := want[v.Candidate.Rule]; !ok || v.Age < d {
			want[v.Candidate.Rule] = v.Age
		}
	}
	if len(fps) != 2 {
		t.Fatalf("got %v want cursor and aider", probeIDs(fps))
	}
	for _, f := range fps {
		// Run reads the clock itself, so allow the drift between the two calls.
		if d := f.Youngest - want[f.Tool.ID]; want[f.Tool.ID] == 0 || d < -time.Minute || d > time.Minute {
			t.Errorf("%s: probe age %v, inspector age %v", f.Tool.ID, f.Youngest, want[f.Tool.ID])
		}
	}
}

func TestProbeIsReadOnly(t *testing.T) {
	root, git, cfg := setup(t)
	testfix.Write(t, filepath.Join(root.Path, "proj/.cursor/a"), "x")
	testfix.Repo(t, filepath.Join(root.Path, "r"))
	testfix.BackdateTree(t, root.Path, 40)
	snap := func() map[string]time.Time {
		m := map[string]time.Time{}
		_ = filepath.WalkDir(root.Path, func(p string, d os.DirEntry, err error) error {
			if err == nil {
				if fi, e := d.Info(); e == nil {
					m[p] = fi.ModTime()
				}
			}
			return nil
		})
		return m
	}
	before := snap()
	if _, err := Probe(context.Background(), cfg, root, git, nil, nil); err != nil {
		t.Fatal(err)
	}
	if after := snap(); !reflect.DeepEqual(before, after) {
		t.Errorf("tree changed during probe")
	}
}
