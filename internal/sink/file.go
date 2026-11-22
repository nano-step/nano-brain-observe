// Package sink writes records to daily-rotating JSONL files.
package sink

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nano-step/nano-brain-observe/internal/record"
)

// File is a record.Sink appending one JSON line per record to
// <dir>/calls-YYYY-MM-DD.jsonl.
type File struct {
	dir string

	mu     sync.Mutex
	f      *os.File
	date   string
	closed bool
}

// NewFile returns a sink writing to <dir>/calls-YYYY-MM-DD.jsonl, creating dir if needed.
func NewFile(dir string) (*File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	return &File{dir: dir}, nil
}

// Write appends r as one JSON line to the file for the local date of r.Time.
func (f *File) Write(r record.Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}
	line = append(line, '\n')

	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	date := t.Local().Format(time.DateOnly)

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("write record: sink closed")
	}
	if f.f == nil || date != f.date {
		nf, err := os.OpenFile(filepath.Join(f.dir, "calls-"+date+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		if f.f != nil {
			f.f.Close()
		}
		f.f, f.date = nf, date
	}
	if _, err := f.f.Write(line); err != nil {
		return fmt.Errorf("write record: %w", err)
	}
	return nil
}

// Close closes the current file. It is idempotent.
func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	if f.f == nil {
		return nil
	}
	if err := f.f.Close(); err != nil {
		return fmt.Errorf("close log file: %w", err)
	}
	return nil
}
