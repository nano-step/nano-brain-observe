// Command nano-brain-observe is a logging reverse proxy for nano-brain.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nano-step/nano-brain-observe/internal/proxy"
	"github.com/nano-step/nano-brain-observe/internal/sink"
)

// Version is set at build time with -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if err := run(); err != nil {
		log.Fatalf("nano-brain-observe: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "stats" {
		return runStats(os.Args[2:], os.Stdout)
	}
	maxDefault, err := strconv.Atoi(envOr("NANO_BRAIN_OBSERVE_MAX_RESPONSE_BYTES", "262144"))
	if err != nil {
		return fmt.Errorf("NANO_BRAIN_OBSERVE_MAX_RESPONSE_BYTES: %w", err)
	}
	listen := flag.String("listen", envOr("NANO_BRAIN_OBSERVE_LISTEN", "127.0.0.1:3101"), "address to listen on")
	upstreamFlag := flag.String("upstream", envOr("NANO_BRAIN_OBSERVE_UPSTREAM", "http://127.0.0.1:3100"), "nano-brain base URL")
	dirFlag := flag.String("dir", envOr("NANO_BRAIN_OBSERVE_DIR", defaultDir), "directory for the JSONL call logs")
	allowedHosts := flag.String("allowed-hosts", "", "comma-separated exact additional Host names or IPs")
	unsafeExposeLAN := flag.Bool("unsafe-expose-lan", false, "allow listening on non-loopback interfaces (unauthenticated)")
	maxResponse := flag.Int("max-response-bytes", maxDefault, "bytes of each response kept in the log")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(Version)
		return nil
	}

	if *maxResponse <= 0 {
		return fmt.Errorf("max-response-bytes must be positive, got %d", *maxResponse)
	}
	hosts, err := proxy.ParseAllowedHosts(*allowedHosts)
	if err != nil {
		return err
	}
	if err := checkListen(*listen, *unsafeExposeLAN); err != nil {
		return err
	}
	upstream, err := url.Parse(*upstreamFlag)
	if err != nil {
		return fmt.Errorf("parse upstream: %w", err)
	}
	if (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.Host == "" {
		return fmt.Errorf("upstream %q must be an http(s) URL with a host", *upstreamFlag)
	}
	same, err := sameEndpoint(*listen, upstream)
	if err != nil {
		return err
	}
	if same {
		return fmt.Errorf("listen %s and upstream %s are the same address; the proxy would call itself", *listen, upstream)
	}
	dir, err := expandHome(*dirFlag)
	if err != nil {
		return err
	}

	files, err := sink.NewFile(dir)
	if err != nil {
		return fmt.Errorf("open log dir: %w", err)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		files.Close()
		return fmt.Errorf("listen: %w", err)
	}
	srv := &http.Server{
		Handler:           proxy.New(proxy.Options{Upstream: upstream, Sink: files, MaxResponseBytes: *maxResponse, AllowedHosts: hosts}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("nano-brain-observe: listening on %s, upstream %s, logging to %s", ln.Addr(), upstream, dir)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case err = <-serveErr:
		err = fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = srv.Shutdown(shutdownCtx); err != nil {
			// Long-lived event streams never go idle; cut them so no
			// handler writes to the sink after it is closed.
			srv.Close()
			if errors.Is(err, context.DeadlineExceeded) {
				err = nil
			} else {
				err = fmt.Errorf("shutdown: %w", err)
			}
		}
	}
	if cerr := files.Close(); cerr != nil && err == nil {
		err = fmt.Errorf("close sink: %w", cerr)
	}
	return err
}

func checkListen(address string, unsafe bool) error {
	// Classify the textual host: resolving here and again in net.Listen could
	// yield different IPs, turning a loopback preflight into a LAN bind.
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("invalid listen port %q", port)
	}
	ip, ipErr := netip.ParseAddr(host)
	loopback := strings.EqualFold(host, "localhost") ||
		ipErr == nil && ip.Zone() == "" && ip.IsLoopback()
	if !unsafe && !loopback {
		return fmt.Errorf("listen %s is not loopback; use --unsafe-expose-lan to allow unauthenticated LAN access", address)
	}
	return nil
}

func expandHome(path string) (string, error) {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand %s: %w", path, err)
	}
	return filepath.Join(home, rest), nil
}

// sameEndpoint reports whether the proxy would be its own upstream: same
// port, and the listen address is that IP, or is a wildcard or loopback
// address while the upstream is loopback (127.0.0.1 and ::1 are one machine).
func sameEndpoint(listen string, upstream *url.URL) (bool, error) {
	l, err := net.ResolveTCPAddr("tcp", listen)
	if err != nil {
		return false, fmt.Errorf("resolve listen address: %w", err)
	}
	host := upstream.Host
	if upstream.Port() == "" {
		port := "80"
		if upstream.Scheme == "https" {
			port = "443"
		}
		host = net.JoinHostPort(upstream.Hostname(), port)
	}
	u, err := net.ResolveTCPAddr("tcp", host)
	if err != nil {
		return false, fmt.Errorf("resolve upstream address: %w", err)
	}
	if l.Port != u.Port {
		return false, nil
	}
	local := l.IP == nil || l.IP.IsUnspecified() || l.IP.IsLoopback()
	return l.IP.Equal(u.IP) || local && u.IP.IsLoopback(), nil
}
