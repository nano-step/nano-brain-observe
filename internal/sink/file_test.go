package sink_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nano-step/nano-brain-observe/internal/record"
	"github.com/nano-step/nano-brain-observe/internal/sink"
)

var _ record.Sink = (*sink.File)(nil)

var day1 = time.Date(2026, 11, 22, 10, 30, 0, 0, time.Local)

func name(t time.Time) string { return "calls-" + t.Format(time.DateOnly) + ".jsonl" }

func newSink(t *testing.T) (*sink.File, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	s, err := sink.NewFile(dir)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func readRecords(t *testing.T, path string) []record.Record {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer fh.Close()
	var out []record.Record
	sc := bufio.NewScanner(fh)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var r record.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", len(out)+1, err)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

func TestWriteRoundTrip(t *testing.T) {
	n := 3
	tests := []struct {
		name string
		rec  record.Record
	}{
		{"minimal", record.Record{Time: day1, Kind: record.KindREST, HTTPMethod: "GET", Path: "/api/v1/status", HTTPStatus: 200, Status: record.StatusOK}},
		{"full", record.Record{
			Time: day1, Kind: record.KindMCPTool, Client: "claude-code/2.0", UserAgent: "ua", SessionID: "s1", Workspace: "ws",
			HTTPMethod: "POST", Path: "/mcp", RPCMethod: "tools/call", Tool: "memory_query",
			Args:       json.RawMessage(`{"query":"a\nb","limit":6}`),
			HTTPStatus: 200, Status: record.StatusError, Error: "boom", LatencyMs: 42,
			ResultCount: &n, ResponseBytes: 11, Response: "line1\nline2", ResponseTruncated: true,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, dir := newSink(t)
			if err := s.Write(tt.rec); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got := readRecords(t, filepath.Join(dir, name(day1)))
			if len(got) != 1 {
				t.Fatalf("got %d lines, want 1", len(got))
			}
			if !got[0].Time.Equal(tt.rec.Time) {
				t.Errorf("time = %v, want %v", got[0].Time, tt.rec.Time)
			}
			want := tt.rec
			got[0].Time, want.Time = time.Time{}, time.Time{}
			if !reflect.DeepEqual(got[0], want) {
				t.Errorf("got %+v, want %+v", got[0], want)
			}
		})
	}
}

func TestZeroTimeUsesNow(t *testing.T) {
	s, dir := newSink(t)
	before := time.Now()
	if err := s.Write(record.Record{Kind: record.KindREST}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	after := time.Now()
	for _, n := range []string{name(before), name(after)} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			return
		}
	}
	t.Fatalf("no file for today's date in %s", dir)
}

func TestPermissions(t *testing.T) {
	s, dir := newSink(t)
	if err := s.Write(record.Record{Time: day1}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	tests := []struct {
		path string
		want os.FileMode
	}{
		{dir, 0o700},
		{filepath.Dir(dir), 0o700},
		{filepath.Join(dir, name(day1)), 0o600},
	}
	for _, tt := range tests {
		fi, err := os.Stat(tt.path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if got := fi.Mode().Perm(); got != tt.want {
			t.Errorf("%s mode = %o, want %o", tt.path, got, tt.want)
		}
	}
}

func TestDateRotation(t *testing.T) {
	s, dir := newSink(t)
	day2 := day1.AddDate(0, 0, 1)
	for _, ts := range []time.Time{day1, day1.Add(time.Hour), day2, day1} {
		if err := s.Write(record.Record{Time: ts}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d files, want 2", len(entries))
	}
	for n, want := range map[string]int{name(day1): 3, name(day2): 1} {
		if got := len(readRecords(t, filepath.Join(dir, n))); got != want {
			t.Errorf("%s has %d lines, want %d", n, got, want)
		}
	}
}

func TestConcurrentWrites(t *testing.T) {
	s, dir := newSink(t)
	const goroutines, writes = 50, 20
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Go(func() {
			for i := range writes {
				r := record.Record{Time: day1, Kind: record.KindMCPTool, SessionID: "g", LatencyMs: int64(g*writes + i), Response: "some response\nbody"}
				if err := s.Write(r); err != nil {
					t.Errorf("Write: %v", err)
				}
			}
		})
	}
	wg.Wait()
	got := readRecords(t, filepath.Join(dir, name(day1)))
	if len(got) != goroutines*writes {
		t.Fatalf("got %d lines, want %d", len(got), goroutines*writes)
	}
	seen := map[int64]bool{}
	for _, r := range got {
		seen[r.LatencyMs] = true
	}
	if len(seen) != goroutines*writes {
		t.Errorf("got %d distinct records, want %d", len(seen), goroutines*writes)
	}
}

func TestClose(t *testing.T) {
	tests := []struct {
		name        string
		writeBefore bool
	}{
		{"never written", false},
		{"after write", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newSink(t)
			if tt.writeBefore {
				if err := s.Write(record.Record{Time: day1}); err != nil {
					t.Fatalf("Write: %v", err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatalf("first Close: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatalf("second Close: %v", err)
			}
			if err := s.Write(record.Record{Time: day1}); err == nil {
				t.Fatal("Write after Close: want error, got nil")
			}
		})
	}
}
