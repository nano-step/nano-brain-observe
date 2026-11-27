package proxy

import (
	"reflect"
	"testing"
)

func TestCountResults(t *testing.T) {
	n := func(v int) *int { return &v }
	tests := []struct {
		name string
		in   string
		want *int
	}{
		{"results", `{"results":[1,2,3],"total":11}`, n(3)},
		{"empty results", `{"results":[]}`, n(0)},
		{"symbols", `{"symbols":[{}]}`, n(1)},
		{"edges", `{"edges":[1,2]}`, n(2)},
		{"nodes", `{"nodes":[1,2,3,4]}`, n(4)},
		{"items", `{"items":[1]}`, n(1)},
		{"documents", `{"documents":[1,2]}`, n(2)},
		{"impacted", `{"count":2,"impacted":[1,2],"node":"x"}`, n(2)},
		{"chain", `{"chain":[1],"count":1,"entry":"x"}`, n(1)},
		{"document with empty tags is not a result list", `{"content":"body","tags":[],"title":"t"}`, nil},
		{"found false", `{"found":false,"message":"not indexed"}`, n(0)},
		{"found true", `{"found":true,"mermaid":"graph"}`, nil},
		{"workspaces", `{"workspaces":[]}`, n(0)},
		{"field order", `{"nodes":[1,2],"edges":[1]}`, n(1)},
		{"non-array field skipped", `{"results":"none","items":[1,2]}`, n(2)},
		{"null field", `{"results":null}`, nil},
		{"top-level array", ` [1,2] `, n(2)},
		{"empty top-level array", `[]`, n(0)},
		{"object without known field", `{"ok":true}`, nil},
		{"plain text", `Saved memory`, nil},
		{"number", `3`, nil},
		{"truncated json", `{"results":[1,2`, nil},
		{"empty", ``, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countResults([]byte(tt.in))
			if (got == nil) != (tt.want == nil) || got != nil && *got != *tt.want {
				t.Errorf("countResults(%q) = %v, want %v", tt.in, deref(got), deref(tt.want))
			}
		})
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestClientFromUserAgent(t *testing.T) {
	tests := []struct{ ua, want string }{
		{"Devin-CLI/3000.15.14 (darwin)", "Devin-CLI/3000.15.14"},
		{"codex-mcp-client/0.172.0", "codex-mcp-client/0.172.0"},
		{"  curl/8.4.0 extra", "curl/8.4.0"},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		if got := clientFromUserAgent(tt.ua); got != tt.want {
			t.Errorf("clientFromUserAgent(%q) = %q, want %q", tt.ua, got, tt.want)
		}
	}
}

func TestSSEData(t *testing.T) {
	in := "event: message\r\nid: a_0\r\ndata: {\"a\":1}\r\n\r\n: comment\ndata: line1\ndata:line2\n\ndata: {\"cut"
	want := []string{`{"a":1}`, "line1\nline2", `{"cut`}
	var got []string
	for _, e := range sseData([]byte(in)) {
		got = append(got, string(e))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sseData = %q, want %q", got, want)
	}
}
