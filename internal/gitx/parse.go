package gitx

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Status is the parsed result of `git status --porcelain=v2 -z --branch ...`.
type Status struct {
	OID       string // HEAD commit, "(initial)" for an unborn branch
	Head      string // branch name, or "(detached)"
	Upstream  string // empty when none
	HasAB     bool   // ahead/behind known (upstream exists and resolves)
	Ahead     int
	Behind    int
	Changed   []string // tracked entries with staged or unstaged changes (incl. conflicts)
	Untracked []string
	Ignored   []string
}

// Detached reports whether HEAD is detached.
func (s Status) Detached() bool { return s.Head == "(detached)" }

// ParseStatusV2 parses NUL-separated porcelain v2 output. Paths are taken verbatim,
// so names containing spaces or newlines are preserved.
func ParseStatusV2(b []byte) (Status, error) {
	var s Status
	recs := splitNUL(b)
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if rec == "" {
			continue
		}
		switch rec[0] {
		case '#':
			if err := s.header(rec); err != nil {
				return Status{}, err
			}
		case '1':
			p, err := field(rec, 8)
			if err != nil {
				return Status{}, err
			}
			s.Changed = append(s.Changed, p)
		case '2':
			p, err := field(rec, 9)
			if err != nil {
				return Status{}, err
			}
			s.Changed = append(s.Changed, p)
			i++ // the original path follows as its own NUL-terminated record
			if i >= len(recs) {
				return Status{}, fmt.Errorf("status: rename record without original path")
			}
		case 'u':
			p, err := field(rec, 10)
			if err != nil {
				return Status{}, err
			}
			s.Changed = append(s.Changed, p)
		case '?':
			s.Untracked = append(s.Untracked, strings.TrimPrefix(rec, "? "))
		case '!':
			s.Ignored = append(s.Ignored, strings.TrimPrefix(rec, "! "))
		default:
			return Status{}, fmt.Errorf("status: unknown record %q", truncate(rec))
		}
	}
	return s, nil
}

func (s *Status) header(rec string) error {
	f := strings.Fields(rec)
	if len(f) < 3 {
		return nil
	}
	switch f[1] {
	case "branch.oid":
		s.OID = f[2]
	case "branch.head":
		s.Head = f[2]
	case "branch.upstream":
		s.Upstream = f[2]
	case "branch.ab":
		if len(f) != 4 {
			return fmt.Errorf("status: bad branch.ab %q", rec)
		}
		a, err1 := strconv.Atoi(strings.TrimPrefix(f[2], "+"))
		b, err2 := strconv.Atoi(strings.TrimPrefix(f[3], "-"))
		if err1 != nil || err2 != nil {
			return fmt.Errorf("status: bad branch.ab %q", rec)
		}
		s.HasAB, s.Ahead, s.Behind = true, a, b
	}
	return nil
}

// field returns everything after the first n space-separated fields.
func field(rec string, n int) (string, error) {
	rest := rec
	for range n {
		i := strings.IndexByte(rest, ' ')
		if i < 0 {
			return "", fmt.Errorf("status: short record %q", truncate(rec))
		}
		rest = rest[i+1:]
	}
	if rest == "" {
		return "", fmt.Errorf("status: empty path in %q", truncate(rec))
	}
	return rest, nil
}

// Worktree is one entry of `git worktree list --porcelain -z`.
type Worktree struct {
	Path           string
	HEAD           string
	Branch         string // full ref, e.g. refs/heads/main; empty when detached
	Bare           bool
	Detached       bool
	Locked         bool
	LockReason     string
	Prunable       bool
	PrunableReason string
}

// ParseWorktrees parses NUL-terminated worktree porcelain output. Records are
// separated by an empty field.
func ParseWorktrees(b []byte) ([]Worktree, error) {
	var out []Worktree
	var cur *Worktree
	for _, f := range splitNUL(b) {
		if f == "" {
			if cur != nil {
				out = append(out, *cur)
				cur = nil
			}
			continue
		}
		key, val, _ := strings.Cut(f, " ")
		if key == "worktree" {
			if cur != nil {
				out = append(out, *cur)
			}
			if val == "" {
				return nil, fmt.Errorf("worktree list: empty path")
			}
			cur = &Worktree{Path: val}
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("worktree list: attribute %q before worktree", truncate(f))
		}
		switch key {
		case "HEAD":
			cur.HEAD = val
		case "branch":
			cur.Branch = val
		case "bare":
			cur.Bare = true
		case "detached":
			cur.Detached = true
		case "locked":
			cur.Locked, cur.LockReason = true, val
		case "prunable":
			cur.Prunable, cur.PrunableReason = true, val
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out, nil
}

// splitNUL splits on NUL, dropping one trailing terminator but keeping empty fields
// in between (they separate worktree records).
func splitNUL(b []byte) []string {
	b = bytes.TrimSuffix(b, []byte{0})
	if len(b) == 0 {
		return nil
	}
	parts := bytes.Split(b, []byte{0})
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = string(p)
	}
	return out
}

func truncate(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
