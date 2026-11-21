package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
)

// defaultProjectRoot is the value omitted from a written file.
const defaultProjectRoot = "~/Projects"

// Changes are the managed values onboarding decided.
type Changes struct {
	ProjectRoot    string // "~/Code" or absolute; "" = default
	ProtectedPaths []string
	Retired        map[string]bool // tool id -> marked retired
}

// writeHooks are test seams; nil in production.
var writeHooks struct {
	beforeRename func() error
}

// Plan returns the bytes to write: existing (may be nil) with the managed keys
// replaced (CFGW-1/2) and validated with LoadBytes (CFGW-3). Tool ids absent from
// ch.Retired are left unchanged.
func Plan(existing []byte, ch Changes) ([]byte, error) {
	const name = "config.toml"
	var rf rawFile
	if len(bytes.TrimSpace(existing)) > 0 {
		r, err := decode(name, existing)
		if err != nil {
			return nil, err
		}
		rf = *r
	}
	one := SupportedVersion
	rf.Version = &one

	rf.ProjectRoot = nil
	if ch.ProjectRoot != "" && ch.ProjectRoot != defaultProjectRoot {
		v := ch.ProjectRoot
		rf.ProjectRoot = &v
	}
	rf.ProtectedPaths = nil
	if len(ch.ProtectedPaths) > 0 {
		v := slices.Clone(ch.ProtectedPaths)
		rf.ProtectedPaths = &v
	}

	base, err := decode("defaults.toml", defaultsTOML)
	if err != nil {
		return nil, fmt.Errorf("embedded defaults are invalid: %w", err)
	}
	defRetired := map[string]bool{}
	for _, t := range base.Retired.Tools {
		defRetired[t.ID] = t.Retired != nil && *t.Retired
	}
	// Tools configured = defaults plus any user-defined ones.
	ids := slices.Clone(slices.Collect(func(y func(string) bool) {
		for _, t := range base.Retired.Tools {
			if !y(t.ID) {
				return
			}
		}
	}))
	if rf.Retired != nil {
		for _, t := range rf.Retired.Tools {
			if !slices.Contains(ids, t.ID) {
				ids = append(ids, t.ID)
			}
		}
	}
	for _, id := range ids {
		want, ok := ch.Retired[id]
		if !ok {
			continue
		}
		setToolRetired(&rf, id, want, defRetired[id])
	}
	if rf.Retired != nil && len(rf.Retired.Tools) == 0 && rf.Retired.Enabled == nil &&
		rf.Retired.Label == nil && rf.Retired.Description == nil &&
		rf.Retired.AgeDays == nil && rf.Retired.SortOrder == nil {
		rf.Retired = nil
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(&rf); err != nil {
		return nil, fmt.Errorf("config: encode: %w", err)
	}
	if _, err := LoadBytes(name, buf.Bytes()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func setToolRetired(rf *rawFile, id string, want, def bool) {
	if rf.Retired == nil {
		rf.Retired = &rawRetired{}
	}
	tools := rf.Retired.Tools
	i := slices.IndexFunc(tools, func(t rawTool) bool { return t.ID == id })
	if want != def {
		if i < 0 {
			tools = append(tools, rawTool{ID: id})
			i = len(tools) - 1
		}
		v := want
		tools[i].Retired = &v
	} else if i >= 0 {
		tools[i].Retired = nil
		if t := tools[i]; t.Name == nil && t.AgeDays == nil && t.Dirs == nil && t.Files == nil && t.Note == nil {
			tools = slices.Delete(tools, i, i+1)
		}
	}
	rf.Retired.Tools = tools
}

// Write backs up an existing target (CFGW-4) and writes data atomically (CFGW-5).
// It returns the backup path ("" if there was no file). It never deletes anything:
// a failed run leaves a fixed-name temp file that the next run truncates.
func Write(path string, data []byte, now time.Time) (backup string, err error) {
	dir, base := filepath.Dir(path), filepath.Base(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	cur, rerr := os.ReadFile(path)
	switch {
	case rerr == nil:
		stamp := base + ".bak-" + now.Local().Format("20060102T150405")
		for n := 1; ; n++ {
			cand := filepath.Join(dir, stamp)
			if n > 1 {
				cand += "-" + strconv.Itoa(n)
			}
			err := writeNew(cand, cur)
			if err == nil {
				backup = cand
				break
			}
			if !errors.Is(err, os.ErrExist) {
				return "", fmt.Errorf("config: backup: %w", err)
			}
		}
	case errors.Is(rerr, os.ErrNotExist):
	default:
		return "", fmt.Errorf("config: %w", rerr)
	}

	tmp := filepath.Join(dir, "."+base+".new")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return backup, fmt.Errorf("config: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return backup, fmt.Errorf("config: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return backup, fmt.Errorf("config: %w", err)
	}
	if err := f.Close(); err != nil {
		return backup, fmt.Errorf("config: %w", err)
	}
	if h := writeHooks.beforeRename; h != nil {
		if err := h(); err != nil {
			return backup, err
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		return backup, fmt.Errorf("config: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		err = d.Sync()
		d.Close()
		if err != nil {
			return backup, fmt.Errorf("config: %w", err)
		}
	}
	return backup, nil
}

// writeNew creates path exclusively (0600), writes data and fsyncs.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
