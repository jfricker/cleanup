// Package runlog appends a JSON Lines record of every --delete run to
// $XDG_STATE_HOME/cleanup/runs.jsonl (SPEC LOG-1…3). It is the only file the
// program writes outside the scan root, and it only creates and appends.
package runlog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileName is the log file inside the state directory.
const FileName = "runs.jsonl"

// Record is one log line.
type Record struct {
	Type    string `json:"type"` // start | item | end
	Run     string `json:"run"`
	TS      string `json:"ts"`
	Root    string `json:"root"`
	Path    string `json:"path,omitempty"`
	Cleaner string `json:"cleaner,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Writer appends records for one run. It is safe for concurrent use.
type Writer struct {
	mu   sync.Mutex
	f    *os.File
	run  string
	root string
	now  func() time.Time
}

// DefaultDir returns $XDG_STATE_HOME/cleanup, or ~/.local/state/cleanup.
func DefaultDir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "cleanup"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("run log: %w", err)
	}
	return filepath.Join(home, ".local", "state", "cleanup"), nil
}

// Open creates dir (0700) and opens the log for appending (0600).
func Open(dir, root string) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("run log: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, FileName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("run log: %w", err)
	}
	return &Writer{f: f, run: newID(), root: root, now: time.Now}, nil
}

// Path returns the log file path.
func (w *Writer) Path() string { return w.f.Name() }

// RunID returns this run's id.
func (w *Writer) RunID() string { return w.run }

// Start writes the start record.
func (w *Writer) Start() error { return w.write(Record{Type: "start"}) }

// Item writes one item record.
func (w *Writer) Item(path, cleaner string, bytes int64, outcome string, err error) error {
	r := Record{Type: "item", Path: path, Cleaner: cleaner, Bytes: bytes, Outcome: outcome}
	if err != nil {
		r.Error = err.Error()
	}
	return w.write(r)
}

// End writes the end record with the total bytes freed and syncs the file.
func (w *Writer) End(bytes int64, outcome string) error {
	err := w.write(Record{Type: "end", Bytes: bytes, Outcome: outcome})
	w.mu.Lock()
	defer w.mu.Unlock()
	return errors.Join(err, w.f.Sync())
}

// Close closes the file.
func (w *Writer) Close() error { return w.f.Close() }

func (w *Writer) write(r Record) error {
	r.Run, r.Root, r.TS = w.run, w.root, w.now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.f.Write(append(b, '\n'))
	return err
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}
