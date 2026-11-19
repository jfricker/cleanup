// Package walk performs the single parallel traversal of the scan root (SPEC WALK-1…5).
// Every directory is read at most once; registered visitors see each listing and
// decide, for themselves only, which children are candidates and which subtrees they
// no longer need. Symlinks are never followed and the walk stays on one device.
package walk

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

// Dir is one directory listing handed to visitors. It must not be modified.
type Dir struct {
	Path    string // absolute canonical path
	Rel     string // path relative to the root ("." for the root)
	Entries []fs.DirEntry
	names   map[string]fs.DirEntry
	anc     uint64 // bit i set: ancestor marker i exists in this dir or an ancestor
	markers []string
	// GitTree is the nearest enclosing git work tree root (this directory itself when
	// it contains .git), or "" when the directory is not inside one.
	GitTree string
}

// Entry returns the child with the given name.
func (d *Dir) Entry(name string) (fs.DirEntry, bool) {
	e, ok := d.names[name]
	return e, ok
}

// Has reports whether a child named name exists.
func (d *Dir) Has(name string) bool {
	_, ok := d.names[name]
	return ok
}

// HasAncestorMarker reports whether marker (one of Options.AncestorMarkers) exists in
// this directory or any ancestor within the root.
func (d *Dir) HasAncestorMarker(marker string) bool {
	i := slices.Index(d.markers, marker)
	return i >= 0 && d.anc&(1<<uint(i)) != 0
}

// Out collects one visitor's decisions about a directory's children.
type Out struct {
	visitor int
	stop    map[string]uint64 // child name → visitors that won't descend
	found   *[]Found
	dir     *Dir
}

// Candidate records child as a candidate. The visitor does not descend into it.
func (o *Out) Candidate(child fs.DirEntry, payload any) {
	*o.found = append(*o.found, Found{
		Visitor: o.visitor,
		Path:    filepath.Join(o.dir.Path, child.Name()),
		Rel:     relJoin(o.dir.Rel, child.Name()),
		IsDir:   child.IsDir(),
		Parent:  o.dir,
		Payload: payload,
	})
	o.Prune(child.Name())
}

// Prune stops this visitor from descending into child name.
func (o *Out) Prune(name string) { o.stop[name] |= 1 << uint(o.visitor) }

// Visitor inspects directory listings. Visit is called concurrently for different
// directories and must be safe for that.
type Visitor interface {
	Visit(d *Dir, out *Out)
}

// Found is a candidate recorded by a visitor.
type Found struct {
	Visitor int
	Path    string
	Rel     string
	IsDir   bool
	Parent  *Dir
	Payload any
}

// GitTree is a directory that contains .git.
type GitTree struct {
	Path   string
	IsRepo bool // .git is a directory (a repository, not a linked worktree or submodule file)
}

// Error is a traversal problem (unreadable or vanished directory).
type Error struct {
	Path string
	Err  error
}

func (e Error) Error() string { return e.Path + ": " + e.Err.Error() }

// Options configure a walk.
type Options struct {
	NeverTouch      []string // names never descended into or reported
	AncestorMarkers []string // names tracked for Dir.HasAncestorMarker (max 64)
	Workers         int      // concurrent ReadDir calls; GOMAXPROCS when 0
	OnDir           func(rel string)
	CountReads      bool // record ReadDir calls per path in Result.Reads
}

// Result is everything a walk produced.
type Result struct {
	Found      []Found
	GitTrees   []GitTree
	Errors     []Error
	Unreadable []string // absolute paths of directories that could not be listed
	Dirs       int64
	Reads      map[string]int
}

// Run walks root (a canonical directory on device dev) with the given visitors.
func Run(ctx context.Context, root string, dev uint64, visitors []Visitor, o Options) (*Result, error) {
	if len(visitors) > 63 || len(o.AncestorMarkers) > 64 {
		panic("walk: too many visitors or ancestor markers")
	}
	w := &walker{
		ctx: ctx, dev: dev, visitors: visitors, o: o,
		never: map[string]bool{},
		sem:   make(chan struct{}, workers(o.Workers)),
		res:   &Result{},
	}
	if o.CountReads {
		w.res.Reads = map[string]int{}
	}
	for _, n := range o.NeverTouch {
		w.never[n] = true
	}
	all := uint64(1)<<uint(len(visitors)) - 1
	w.wg.Add(1)
	go w.dir(root, ".", all, 0, "")
	w.wg.Wait()
	slices.SortFunc(w.res.Found, func(a, b Found) int { return cmpStr(a.Path, b.Path) })
	slices.SortFunc(w.res.GitTrees, func(a, b GitTree) int { return cmpStr(a.Path, b.Path) })
	slices.SortFunc(w.res.Errors, func(a, b Error) int { return cmpStr(a.Path, b.Path) })
	slices.Sort(w.res.Unreadable)
	w.res.Dirs = w.dirs.Load()
	return w.res, ctx.Err()
}

type walker struct {
	ctx      context.Context
	dev      uint64
	visitors []Visitor
	o        Options
	never    map[string]bool
	sem      chan struct{}
	wg       sync.WaitGroup
	dirs     atomic.Int64
	mu       sync.Mutex
	res      *Result
}

func (w *walker) dir(path, rel string, mask, anc uint64, gitTree string) {
	defer w.wg.Done()
	if w.ctx.Err() != nil {
		return
	}
	w.sem <- struct{}{}
	entries, err := os.ReadDir(path)
	<-w.sem
	w.dirs.Add(1)
	if w.o.OnDir != nil {
		w.o.OnDir(rel)
	}
	if err != nil {
		w.mu.Lock()
		w.res.Errors = append(w.res.Errors, Error{Path: path, Err: err})
		w.res.Unreadable = append(w.res.Unreadable, path)
		w.mu.Unlock()
		return
	}

	d := &Dir{Path: path, Rel: rel, Entries: entries, markers: w.o.AncestorMarkers,
		names: make(map[string]fs.DirEntry, len(entries)), GitTree: gitTree}
	for _, e := range entries {
		d.names[e.Name()] = e
	}
	for i, m := range w.o.AncestorMarkers {
		if d.Has(m) {
			anc |= 1 << uint(i)
		}
	}
	d.anc = anc
	var tree *GitTree
	if g, ok := d.names[".git"]; ok {
		d.GitTree = path
		tree = &GitTree{Path: path, IsRepo: g.IsDir()}
	}

	var found []Found
	stop := map[string]uint64{}
	for i, v := range w.visitors {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		v.Visit(d, &Out{visitor: i, stop: stop, found: &found, dir: d})
	}
	// A visitor may not record never-touch entries or symlinks (WALK-2, CFG-6).
	found = slices.DeleteFunc(found, func(f Found) bool {
		e := d.names[filepath.Base(f.Path)]
		return w.never[e.Name()] || e.Type()&fs.ModeSymlink != 0
	})

	w.mu.Lock()
	w.res.Found = append(w.res.Found, found...)
	if tree != nil {
		w.res.GitTrees = append(w.res.GitTrees, *tree)
	}
	if w.res.Reads != nil {
		w.res.Reads[path]++
	}
	w.mu.Unlock()

	for _, e := range entries {
		if !e.IsDir() || e.Type()&fs.ModeSymlink != 0 || w.never[e.Name()] {
			continue
		}
		childMask := mask &^ stop[e.Name()]
		if childMask == 0 {
			continue
		}
		child := filepath.Join(path, e.Name())
		if !w.sameDevice(e) {
			continue // WALK-4: never cross onto another filesystem
		}
		w.wg.Add(1)
		go w.dir(child, relJoin(rel, e.Name()), childMask, anc, d.GitTree)
	}
}

func (w *walker) sameDevice(e fs.DirEntry) bool {
	if w.dev == 0 {
		return true
	}
	fi, err := e.Info()
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && uint64(st.Dev) == w.dev // int32 on darwin, uint64 on linux
}

func relJoin(rel, name string) string {
	if rel == "." {
		return name
	}
	return rel + "/" + name
}

func workers(n int) int {
	if n > 0 {
		return n
	}
	return runtime.GOMAXPROCS(0)
}

func cmpStr(a, b string) int { return strings.Compare(a, b) }
