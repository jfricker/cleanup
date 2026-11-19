package safety

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// OutcomeKind is the result of one deletion.
type OutcomeKind int

// Outcome kinds.
const (
	Deleted    OutcomeKind = iota + 1
	NotDeleted             // skipped at revalidation
	Failed
	Partial
)

func (k OutcomeKind) String() string {
	switch k {
	case Deleted:
		return "deleted"
	case NotDeleted:
		return "skipped"
	case Failed:
		return "failed"
	case Partial:
		return "partially deleted"
	}
	return "invalid"
}

// Outcome describes one deletion.
type Outcome struct {
	Kind   OutcomeKind
	Bytes  int64
	Reason *Reason // for NotDeleted
	Err    error   // for Failed and Partial
}

// Deleter removes verified targets. It is the only code in the program that removes
// files (SPEC NFR-4).
type Deleter struct {
	in *Inspector
}

// NewDeleter returns a deleter that revalidates with in.
func NewDeleter(in *Inspector) *Deleter { return &Deleter{in: in} }

// ErrInvalidTarget is returned for a zero Target.
var ErrInvalidTarget = errors.New("not a verified target")

// Delete revalidates t and removes it (SPEC DEL-1…3). It never falls back to
// removing files itself when git refuses a worktree removal.
func (d *Deleter) Delete(ctx context.Context, t Target) Outcome {
	if t.v == nil {
		return Outcome{Kind: Failed, Err: ErrInvalidTarget}
	}
	v := t.v
	if r := d.revalidate(ctx, v); r != nil {
		return Outcome{Kind: NotDeleted, Reason: r}
	}
	root := d.in.root.FS
	var err error
	switch v.kind {
	case TargetRemove:
		err = root.RemoveAll(v.rel)
	case TargetEmptyContents:
		err = emptyContents(root, v.rel)
	case TargetWorktree:
		if err = d.in.git.WorktreeRemove(ctx, v.cand.Repo, v.abs, v.force); err != nil {
			return Outcome{Kind: Failed, Err: fmt.Errorf("git refused: %w", err)}
		}
	default:
		return Outcome{Kind: Failed, Err: ErrInvalidTarget}
	}
	if err != nil {
		if _, statErr := root.Lstat(v.rel); statErr == nil && v.kind == TargetRemove {
			return Outcome{Kind: Partial, Err: err}
		}
		return Outcome{Kind: Failed, Err: err}
	}
	if v.kind != TargetEmptyContents {
		if _, err := root.Lstat(v.rel); err == nil {
			return Outcome{Kind: Partial, Err: errors.New("path still exists after removal")}
		}
	}
	return Outcome{Kind: Deleted, Bytes: v.size}
}

// revalidate re-checks identity, containment and git state (SPEC DEL-2).
func (d *Deleter) revalidate(ctx context.Context, v *verified) *Reason {
	changed := func(detail string) *Reason { return &Reason{Code: ReasonChangedSinceScan, Detail: detail} }
	canon, err := filepath.EvalSymlinks(v.abs)
	if err != nil {
		return changed(err.Error())
	}
	if canon != v.abs {
		return changed("path now resolves to " + canon)
	}
	if _, ok := relUnder(d.in.root.Path, canon); !ok {
		return changed("no longer under the root")
	}
	fi, err := d.in.root.FS.Lstat(v.rel)
	if err != nil {
		return changed(err.Error())
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return changed("now a symlink")
	}
	if dev, ino := identity(fi); dev != v.dev || ino != v.ino || fi.IsDir() != v.isDir {
		return changed("replaced by a different file")
	}
	for _, s := range v.trees {
		ok, why, err := d.in.unchanged(ctx, s)
		if err != nil {
			return changed(err.Error())
		}
		if !ok {
			return changed(why)
		}
	}
	return nil
}

func emptyContents(root *os.Root, rel string) error {
	sub, err := root.OpenRoot(rel)
	if err != nil {
		return err
	}
	defer sub.Close()
	entries, err := fs.ReadDir(sub.FS(), ".")
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if err := sub.RemoveAll(e.Name()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
