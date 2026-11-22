// Package proxy is a transparent reverse proxy in front of nano-brain that
// records every call (MCP JSON-RPC or REST) to a record.Sink.
package proxy

import (
	"bytes"
	"context"
	"io"
	"log"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nano-step/nano-brain-observe/internal/record"
)

const (
	defaultMaxResponseBytes = 256 << 10
	// maxRequestBytes is how much of a request body is kept for logging; a
	// larger body is still forwarded in full but logged without args.
	maxRequestBytes = 8 << 20
	// parseBytes is how much of a response is held in memory to derive
	// status and result count, independent of the (often smaller) log cap.
	parseBytes = 4 << 20
	maxClients = 1024
	sessionHdr = "Mcp-Session-Id"
)

// Options configures New.
type Options struct {
	Upstream         *url.URL
	Sink             record.Sink
	MaxResponseBytes int         // cap for Record.Response; 0 => 256<<10
	Logger           *log.Logger // for sink/internal errors; nil => log.Default()
}

type handler struct {
	rp      *httputil.ReverseProxy
	sink    record.Sink
	maxResp int
	log     *log.Logger

	mu      sync.Mutex
	clients map[string]string // session id -> "name/version"
}

// New returns a handler that forwards every request to opts.Upstream and
// writes one or more records per request to opts.Sink.
func New(opts Options) http.Handler {
	h := &handler{
		sink:    opts.Sink,
		maxResp: opts.MaxResponseBytes,
		log:     opts.Logger,
		clients: map[string]string{},
	}
	if h.maxResp == 0 {
		h.maxResp = defaultMaxResponseBytes
	}
	if h.log == nil {
		h.log = log.Default()
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Without this the transport asks for gzip itself once Accept-Encoding
	// is stripped below.
	transport.DisableCompression = true
	h.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(opts.Upstream)
			pr.Out.Header.Del("Accept-Encoding")
		},
		Transport:      transport,
		FlushInterval:  -1,
		ModifyResponse: h.modifyResponse,
		ErrorHandler:   h.errorHandler,
		ErrorLog:       h.log,
	}
	return h
}

type callKey struct{}

// call is the capture state of one proxied request. It is only touched by
// the goroutine serving that request.
type call struct {
	h     *handler
	start time.Time
	req   *http.Request
	body  []byte       // request body; nil when absent or over maxRequestBytes
	rpcs  []rpcMessage // JSON-RPC requests found in body on an MCP path

	status    int
	header    http.Header
	err       error  // upstream round-trip failure
	mediaType string // response media type
	logBody   bool   // response body may be stored in Record.Response
	captured  []byte // response body prefix, at most the teeBody limit
	total     int    // response body bytes seen
	done      bool
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mcp := isMCPPath(r.URL.Path)
	if mcp && r.Method == http.MethodGet {
		// Long-lived server->client event stream: pass through, no record.
		h.rp.ServeHTTP(w, r)
		return
	}
	c := &call{h: h, start: time.Now()}
	r = r.WithContext(context.WithValue(r.Context(), callKey{}, c))
	c.req = r
	if r.Body != nil && r.Body != http.NoBody {
		// A read error is not handled here: the remaining r.Body returns
		// it again to the transport, which fails the request as usual.
		kept, _ := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(kept), r.Body), r.Body}
		if len(kept) <= maxRequestBytes {
			c.body = kept
		}
	}
	if mcp {
		for _, m := range parseRPC(c.body) {
			if m.Method != "" {
				c.rpcs = append(c.rpcs, m)
			}
		}
	}
	h.rp.ServeHTTP(w, r)
}

func (h *handler) modifyResponse(resp *http.Response) error {
	c, _ := resp.Request.Context().Value(callKey{}).(*call)
	// An upgraded connection needs the original read-write body.
	if c == nil || resp.StatusCode == http.StatusSwitchingProtocols {
		return nil
	}
	c.status, c.header = resp.StatusCode, resp.Header
	c.mediaType, _, _ = mime.ParseMediaType(resp.Header.Get("Content-Type"))
	c.logBody = resp.Header.Get("Content-Encoding") == "" &&
		(isLoggable(c.mediaType) || len(c.rpcs) > 0 && c.mediaType == "text/event-stream")
	limit := 0
	if c.logBody {
		limit = max(h.maxResp, parseBytes)
	}
	resp.Body = &teeBody{rc: resp.Body, c: c, limit: limit}
	return nil
}

func (h *handler) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Printf("proxy %s %s: %v", r.Method, r.URL.Path, err)
	w.WriteHeader(http.StatusBadGateway)
	if c, _ := r.Context().Value(callKey{}).(*call); c != nil {
		c.status, c.err = http.StatusBadGateway, err
		c.finish()
	}
}

// finish emits the call's records once, when the response body is done.
func (c *call) finish() {
	if c.done {
		return
	}
	c.done = true
	for _, rec := range c.records(time.Since(c.start)) {
		if err := c.h.sink.Write(rec); err != nil {
			c.h.log.Printf("write record: %v", err)
		}
	}
}

// teeBody copies up to limit bytes of the response into the call while the
// reverse proxy streams it to the client.
type teeBody struct {
	rc    io.ReadCloser
	c     *call
	limit int
}

func (b *teeBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.c.total += n
	if room := b.limit - len(b.c.captured); room > 0 {
		b.c.captured = append(b.c.captured, p[:min(n, room)]...)
	}
	if err != nil {
		b.c.finish()
	}
	return n, err
}

func (b *teeBody) Close() error {
	err := b.rc.Close()
	b.c.finish()
	return err
}

func (h *handler) client(sessionID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clients[sessionID]
}

func (h *handler) rememberClient(sessionID, client string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= maxClients {
		clear(h.clients)
	}
	h.clients[sessionID] = client
}

func isMCPPath(p string) bool {
	p = strings.TrimSuffix(p, "/")
	return p == "/mcp" || p == "/sse"
}

func isJSON(mediaType string) bool {
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// isLoggable reports whether a response of this media type is worth storing
// as text: JSON and plain text, not web UI assets or event streams.
func isLoggable(mediaType string) bool {
	if isJSON(mediaType) {
		return true
	}
	switch mediaType {
	case "text/html", "text/css", "text/javascript", "text/event-stream":
		return false
	}
	return strings.HasPrefix(mediaType, "text/")
}
