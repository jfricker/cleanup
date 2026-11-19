package gitx

import (
	"reflect"
	"strings"
	"testing"
)

func nul(parts ...string) []byte { return []byte(strings.Join(parts, "\x00") + "\x00") }

func TestParseStatusV2(t *testing.T) {
	in := nul(
		"# branch.oid abc",
		"# branch.head feat",
		"# branch.upstream origin/feat",
		"# branch.ab +2 -1",
		"1 .M N... 100644 100644 100644 h1 h2 dir/a file.txt",
		"2 R. N... 100644 100644 100644 h1 h2 R100 new name", "old name",
		"u UU N... 100644 100644 100644 100644 h1 h2 h3 conflict",
		"? un\ntracked",
		"! .env",
	)
	got, err := ParseStatusV2(in)
	if err != nil {
		t.Fatal(err)
	}
	want := Status{
		OID: "abc", Head: "feat", Upstream: "origin/feat", HasAB: true, Ahead: 2, Behind: 1,
		Changed:   []string{"dir/a file.txt", "new name", "conflict"},
		Untracked: []string{"un\ntracked"},
		Ignored:   []string{".env"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParseStatusV2Errors(t *testing.T) {
	for _, in := range [][]byte{
		nul("1 .M short"),
		nul("2 R. N... 100644 100644 100644 h1 h2 R100 new"),
		nul("X what"),
		nul("# branch.ab +x -1"),
	} {
		if _, err := ParseStatusV2(in); err == nil {
			t.Errorf("want error for %q", in)
		}
	}
}

func TestParseWorktrees(t *testing.T) {
	in := []byte("worktree /r\x00HEAD aaa\x00branch refs/heads/main\x00\x00" +
		"worktree /w\x00HEAD bbb\x00detached\x00locked busy now\x00\x00" +
		"worktree /gone\x00HEAD ccc\x00branch refs/heads/x\x00prunable gitdir file points to non-existent location\x00\x00")
	got, err := ParseWorktrees(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []Worktree{
		{Path: "/r", HEAD: "aaa", Branch: "refs/heads/main"},
		{Path: "/w", HEAD: "bbb", Detached: true, Locked: true, LockReason: "busy now"},
		{Path: "/gone", HEAD: "ccc", Branch: "refs/heads/x", Prunable: true,
			PrunableReason: "gitdir file points to non-existent location"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func FuzzParseStatusV2(f *testing.F) {
	f.Add(nul("# branch.head main", "? a", "1 .M N... 1 1 1 h h p"))
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = ParseStatusV2(b) })
}

func FuzzParseWorktrees(f *testing.F) {
	f.Add([]byte("worktree /r\x00HEAD a\x00\x00"))
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = ParseWorktrees(b) })
}
