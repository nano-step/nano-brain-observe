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

func TestCheckListen(t *testing.T) {
	for _, address := range []string{
		":3101", "0.0.0.0:3101", "[::]:3101", "192.168.1.5:3101",
		// Even when this alias resolves to loopback, its text is not a
		// loopback literal or the special name localhost.
		"localhost.localdomain:3101", "LOCALHOST.:3101", "127.1:3101",
		"[::1%lo0]:3101",
	} {
		if err := checkListen(address, false); err == nil {
			t.Errorf("checkListen(%q, false) accepted non-loopback", address)
		}
		if err := checkListen(address, true); err != nil {
			t.Errorf("checkListen(%q, true): %v", address, err)
		}
	}
	for _, address := range []string{
		"127.0.0.1:3101", "127.42.3.2:0", "[::1]:3101",
		"localhost:3101", "LOCALHOST:3101",
	} {
		if err := checkListen(address, false); err != nil {
			t.Errorf("checkListen(%q, false): %v", address, err)
		}
	}
	for _, address := range []string{
		"localhost", "localhost:", "localhost:bad", "localhost:65536",
		"localhost:-1", "[::1:3101", "::1:3101",
	} {
		for _, unsafe := range []bool{false, true} {
			if err := checkListen(address, unsafe); err == nil {
				t.Errorf("checkListen(%q, %v) accepted malformed address", address, unsafe)
			}
		}
	}
}
