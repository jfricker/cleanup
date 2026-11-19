package safety

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
)

// subtree is one pass over a candidate's contents through the root handle.
type subtree struct {
	size       int64
	newest     time.Time // newest mtime of any entry outside .git (deep age)
	gitTrees   []string  // slash paths relative to the root of dirs containing .git
	neverTouch string    // first never-touch entry found (other than .git)
	unreadable string    // first directory that could not be listed
}

// scanSubtree walks rel (a directory or file under root) without following symlinks.
// skip, when non-nil, excludes slash paths (relative to rel) from the newest-mtime
// calculation only.
func scanSubtree(root *os.Root, rel string, isDir bool, never map[string]bool, skip func(string) bool) (subtree, error) {
	var st subtree
	fi, err := root.Lstat(rel)
	if err != nil {
		return st, err
	}
	st.newest = fi.ModTime()
	if !isDir {
		st.size = fi.Size()
		return st, nil
	}
	fsys := root.FS()
	err = fs.WalkDir(fsys, rel, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() || errors.Is(err, fs.ErrPermission) {
				if st.unreadable == "" {
					st.unreadable = p
				}
				return fs.SkipDir
			}
			return err
		}
		name := d.Name()
		inGit := strings.Contains("/"+p+"/", "/.git/")
		if d.IsDir() && p != rel {
			if name == ".git" {
				st.gitTrees = append(st.gitTrees, path.Dir(p))
			} else if never[name] && !inGit && st.neverTouch == "" {
				st.neverTouch = p
			}
		} else if name == ".git" && d.Type().IsRegular() {
			st.gitTrees = append(st.gitTrees, path.Dir(p)) // linked worktree or submodule
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // vanished during the scan
			}
			return err
		}
		if info.Mode().IsRegular() {
			st.size += info.Size()
		}
		if inGit || name == ".git" || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if skip != nil && p != rel && skip(strings.TrimPrefix(p, rel+"/")) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if info.ModTime().After(st.newest) {
			st.newest = info.ModTime()
		}
		return nil
	})
	return st, err
}

// shallowNewest returns the newest mtime of rel itself and its direct children.
func shallowNewest(root *os.Root, rel string, isDir bool) (time.Time, error) {
	fi, err := root.Lstat(rel)
	if err != nil {
		return time.Time{}, err
	}
	newest := fi.ModTime()
	if !isDir {
		return newest, nil
	}
	entries, err := fs.ReadDir(root.FS(), rel)
	if err != nil {
		return time.Time{}, err
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return time.Time{}, err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, nil
}
