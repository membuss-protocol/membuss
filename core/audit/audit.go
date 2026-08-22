// Package audit provides an append-only JSONL audit trail for
// destructive administrative operations (finding.txt XC-008):
// content deletes, node flash (DropAll), keyring removals.
//
// The log lives at <datadir>/logs/audit.jsonl. Every entry records
// who (client IP / peer), when, what action, and the target.
package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one audited event.
type Entry struct {
	Time   time.Time         `json:"time"`
	Actor  string            `json:"actor"`  // client IP or peer ID
	Action string            `json:"action"` // e.g. "delete", "drop_all"
	Target string            `json:"target,omitempty"`
	Detail map[string]string `json:"detail,omitempty"`
}

// Logger appends entries to the audit JSONL file.
// A nil *Logger is valid and discards everything, so call sites
// never need nil checks.
type Logger struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

const maxLogBytes = 10 << 20 // rotate past 10 MiB

// Open creates (or appends to) the audit log under dataDir/logs/.
func Open(dataDir string) (*Logger, error) {
	if filepath.Clean(dataDir) == "" {
		return nil, errors.New("audit: empty datadir")
	}
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "audit.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, err
	}
	return &Logger{path: path, f: f}, nil
}

// Log appends one entry. Best-effort by design: audit failures must
// not break the destructive operation itself, but the error is
// returned so callers can log it.
func (l *Logger) Log(actor, action, target string, detail map[string]string) error {
	if l == nil {
		return nil
	}
	e := Entry{Time: time.Now().UTC(), Actor: actor, Action: action, Target: target, Detail: detail}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return errors.New("audit: closed")
	}
	if st, serr := l.f.Stat(); serr == nil && st.Size()+int64(len(b)) > maxLogBytes {
		l.rotateLocked()
	}
	if _, err := l.f.Write(b); err != nil {
		return err
	}
	return nil
}

// rotateLocked renames the current log aside and reopens a fresh one.
// The old file is left in place (single .1 generation).
func (l *Logger) rotateLocked() {
	_ = l.f.Close()
	_ = os.Rename(l.path, l.path+".1")
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		l.f = nil
		return
	}
	l.f = f
}

// Close flushes and closes the log file. Nil-safe.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Tail returns the last n entries, oldest first. Nil-safe (returns nil).
func (l *Logger) Tail(n int) ([]Entry, error) {
	if l == nil || n <= 0 {
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := os.ReadFile(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var entries []Entry
	for _, line := range bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n")) {
		var e Entry
		if json.Unmarshal(line, &e) == nil {
			entries = append(entries, e)
		}
	}
	if len(entries) > n {
		entries = entries[len(entries)-n:]
	}
	return entries, nil
}
