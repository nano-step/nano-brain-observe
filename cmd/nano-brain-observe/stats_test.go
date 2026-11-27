package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseSince(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"7d", 7 * 24 * time.Hour, false},
		{"36h", 36 * time.Hour, false},
		{"90m", 90 * time.Minute, false},
		{"0d", 0, false},
		{"xd", 0, true},
		{"-1d", 0, true},
		{"", 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		got, err := parseSince(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseSince(%q) = %v, %v; want %v, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestPercentile(t *testing.T) {
	ten := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	tests := []struct {
		in   []int64
		p    int
		want int64
	}{
		{ten, 50, 5},
		{ten, 95, 10},
		{[]int64{7}, 50, 7},
		{[]int64{7}, 95, 7},
		{nil, 50, 0},
	}
	for _, tt := range tests {
		if got := percentile(tt.in, tt.p); got != tt.want {
			t.Errorf("percentile(%v, %d) = %d, want %d", tt.in, tt.p, got, tt.want)
		}
	}
}

func TestRunStats(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	ts := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339Nano) }
	line := func(ago time.Duration, kind, tool, client, status string, ms int) string {
		return `{"ts":"` + ts(ago) + `","kind":"` + kind + `","tool":"` + tool + `","client":"` + client +
			`","status":"` + status + `","latency_ms":` + strconv.Itoa(ms) + "}\n"
	}
	day1 := line(time.Hour, "mcp_tool", "memory_query", "codex/1", "ok", 100) +
		line(time.Hour, "mcp_tool", "memory_query", "codex/1", "empty", 200) +
		line(time.Hour, "mcp_tool", "memory_query", "", "error", 300) +
		line(time.Hour, "mcp_tool", "memory_query", "codex/1", "ok", 400) +
		line(time.Hour, "mcp_tool", "memory_write", "codex/1", "ok", 50) +
		line(time.Hour, "rest", "", "", "ok", 999) +
		line(time.Hour, "mcp", "", "", "ok", 999) +
		"not json\n"
	day2 := line(48*time.Hour, "mcp_tool", "memory_write", "old/1", "ok", 999) +
		line(2*time.Hour, "mcp_tool", "memory_write", "codex/1", "error", 150) +
		"{broken\n"
	for name, body := range map[string]string{"calls-2026-01-01.jsonl": day1, "calls-2026-01-02.jsonl": day2, "other.jsonl": "ignored"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := runStats([]string{"-dir", dir, "-since", "24h"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"window 24h, 2 files, 6 records, 2 malformed lines",
		"memory_query  4      1      1      200     400     400",
		"memory_write  2      1      0      50      150     150",
		"codex/1    5      1",
		"(unknown)  1      1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old/1") {
		t.Errorf("record outside window counted:\n%s", got)
	}
	if strings.Index(got, "memory_query") > strings.Index(got, "memory_write") {
		t.Errorf("tools not sorted by calls desc:\n%s", got)
	}
}

func TestSafe(t *testing.T) {
	for in, want := range map[string]string{
		"codex/1":                "codex/1",
		"evil\x1b]0;pwned\x07/1": "evil]0;pwned/1",
		"tab\there":              "tabhere",
		strings.Repeat("a", 70):  strings.Repeat("a", 60),
	} {
		if got := safe(in); got != want {
			t.Errorf("safe(%q) = %q, want %q", in, got, want)
		}
	}
}
