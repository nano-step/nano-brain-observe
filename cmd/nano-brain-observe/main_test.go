package main

import (
	"net/url"
	"testing"
)

func TestSameEndpoint(t *testing.T) {
	tests := []struct {
		listen, upstream string
		want             bool
	}{
		{"127.0.0.1:3101", "http://127.0.0.1:3100", false},
		{"127.0.0.1:3100", "http://127.0.0.1:3100", true},
		{":3100", "http://127.0.0.1:3100", true},
		{"0.0.0.0:3100", "http://127.0.0.1:3100/", true},
		{"127.0.0.1:80", "http://127.0.0.1", true},
		{"127.0.0.1:443", "https://127.0.0.1", true},
		{"127.0.0.1:3100", "http://[::1]:3100", true},
		{"[::1]:3100", "http://127.0.0.1:3100", true},
		{"127.0.0.1:3100", "http://10.0.0.5:3100", false},
		{":3100", "http://10.0.0.5:3100", false},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.upstream)
		if err != nil {
			t.Fatal(err)
		}
		got, err := sameEndpoint(tt.listen, u)
		if err != nil {
			t.Fatalf("sameEndpoint(%q, %q): %v", tt.listen, tt.upstream, err)
		}
		if got != tt.want {
			t.Errorf("sameEndpoint(%q, %q) = %v, want %v", tt.listen, tt.upstream, got, tt.want)
		}
	}
}
