package proxy_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nano-step/nano-brain-observe/internal/proxy"
	"github.com/nano-step/nano-brain-observe/internal/record"
)

type memSink struct {
	mu   sync.Mutex
	recs []record.Record
	err  error
}

func (s *memSink) Write(r record.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, r)
	return s.err
}

func (s *memSink) Close() error { return nil }

// wait returns once exactly n records have arrived; records are written
// after the body is done, which can be just after the client has read it.
func (s *memSink) wait(t *testing.T, n int) []record.Record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		recs := append([]record.Record(nil), s.recs...)
		s.mu.Unlock()
		if len(recs) == n {
			return recs
		}
		if len(recs) > n || time.Now().After(deadline) {
			t.Fatalf("got %d records, want %d: %+v", len(recs), n, recs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// start runs a fake upstream and a proxy in front of it, returning the
// proxy's base URL.
func start(t *testing.T, upstream http.Handler, maxResponseBytes int) (string, *memSink) {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	return startProxy(t, up.URL, &memSink{}, maxResponseBytes)
}

func startProxy(t *testing.T, upstreamURL string, s *memSink, maxResponseBytes int) (string, *memSink) {
	t.Helper()
	u, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatal(err)
	}
	px := httptest.NewServer(proxy.New(proxy.Options{
		Upstream:         u,
		Sink:             s,
		MaxResponseBytes: maxResponseBytes,
		Logger:           log.New(io.Discard, "", 0),
	}))
	t.Cleanup(px.Close)
	return px.URL, s
}

func sse(messages ...string) string {
	var b strings.Builder
	for i, m := range messages {
		fmt.Fprintf(&b, "event: message\nid: ISF2_%d\ndata: %s\n\n", i, m)
	}
	return b.String()
}

func serve(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		io.WriteString(w, body)
	}
}

func do(t *testing.T, method, target, body string, header map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(got)
}

func post(t *testing.T, target, body string) (*http.Response, string) {
	t.Helper()
	return do(t, http.MethodPost, target, body, map[string]string{"Content-Type": "application/json"})
}

func toolCall(id int, args string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"memory_query","arguments":%s}}`, id, args)
}

func toolResult(id int, text string) (message, result string) {
	quoted, _ := json.Marshal(text)
	result = fmt.Sprintf(`{"content":[{"type":"text","text":%s}]}`, quoted)
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, id, result), result
}

func count(t *testing.T, r record.Record) int {
	t.Helper()
	if r.ResultCount == nil {
		t.Fatalf("ResultCount = nil, record %+v", r)
	}
	return *r.ResultCount
}

func TestSSEToolCall(t *testing.T) {
	args := `{"workspace":"ws1","query":"auth flow"}`
	reqBody := toolCall(2, args)
	msg, result := toolResult(2, `{"results":[{"id":"a"},{"id":"b"}],"total":11}`)
	respBody := sse(msg)

	base, sink := start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if string(got) != reqBody || r.URL.RequestURI() != "/mcp?x=1&y=a%20b" {
			t.Errorf("upstream got %s %q", r.URL.RequestURI(), got)
		}
		if ae := r.Header.Get("Accept-Encoding"); ae != "" {
			t.Errorf("upstream Accept-Encoding = %q, want none", ae)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Upstream", "yes")
		io.WriteString(w, respBody)
	}), 0)

	before := time.Now()
	resp, body := do(t, http.MethodPost, base+"/mcp?x=1&y=a%20b", reqBody, map[string]string{
		"Content-Type":    "application/json",
		"User-Agent":      "claude-code/2.0",
		"Accept-Encoding": "gzip",
	})
	if resp.StatusCode != 200 || body != respBody {
		t.Fatalf("client got %d %q, want 200 %q", resp.StatusCode, body, respBody)
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("X-Upstream") != "yes" {
		t.Errorf("client headers = %v", resp.Header)
	}

	r := sink.wait(t, 1)[0]
	want := record.Record{
		Time: r.Time, LatencyMs: r.LatencyMs, ResultCount: r.ResultCount,
		Kind:          record.KindMCPTool,
		UserAgent:     "claude-code/2.0",
		Workspace:     "ws1",
		HTTPMethod:    "POST",
		Path:          "/mcp",
		RPCMethod:     "tools/call",
		Tool:          "memory_query",
		Args:          json.RawMessage(args),
		HTTPStatus:    200,
		Status:        record.StatusOK,
		ResponseBytes: len(result),
		Response:      result,
	}
	if fmt.Sprintf("%+v", r) != fmt.Sprintf("%+v", want) {
		t.Errorf("record =\n%+v\nwant\n%+v", r, want)
	}
	if count(t, r) != 2 {
		t.Errorf("ResultCount = %d, want 2", count(t, r))
	}
	if r.Time.Before(before) || r.Time.After(time.Now()) || r.LatencyMs < 0 {
		t.Errorf("Time = %v, LatencyMs = %d", r.Time, r.LatencyMs)
	}
}

func TestJSONResponse(t *testing.T) {
	msg, result := toolResult(7, `{"symbols":[1,2,3]}`)
	base, sink := start(t, serve("application/json; charset=utf-8", msg), 0)

	_, body := post(t, base+"/mcp", toolCall(7, `{"workspace":"w"}`))
	if body != msg {
		t.Fatalf("client got %q, want %q", body, msg)
	}
	r := sink.wait(t, 1)[0]
	if r.Kind != record.KindMCPTool || r.Response != result || count(t, r) != 3 || r.Status != record.StatusOK {
		t.Errorf("record = %+v", r)
	}
}

func TestBatch(t *testing.T) {
	reqBody := "[" + strings.Join([]string{
		toolCall(1, `{"workspace":"w","query":"q"}`),
		`{"jsonrpc":"2.0","id":"a","method":"tools/list","params":{"cursor":"c"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	}, ",") + "]"
	msg1, result1 := toolResult(1, `{"results":[1]}`)
	// Responses arrive out of order and split over a batch and a single event.
	respBody := sse(`[{"jsonrpc":"2.0","id":"a","result":{"tools":[]}}]`, msg1)
	base, sink := start(t, serve("text/event-stream", respBody), 0)

	if _, body := post(t, base+"/mcp", reqBody); body != respBody {
		t.Fatalf("client got %q, want %q", body, respBody)
	}
	recs := sink.wait(t, 3)

	if r := recs[0]; r.Kind != record.KindMCPTool || r.Tool != "memory_query" || r.Response != result1 || count(t, r) != 1 {
		t.Errorf("record 0 = %+v", r)
	}
	if r := recs[1]; r.Kind != record.KindMCP || r.RPCMethod != "tools/list" || r.Tool != "" ||
		string(r.Args) != `{"cursor":"c"}` || r.Response != `{"tools":[]}` || r.ResultCount != nil || r.Status != record.StatusOK {
		t.Errorf("record 1 = %+v", r)
	}
	if r := recs[2]; r.Kind != record.KindMCP || r.RPCMethod != "notifications/initialized" ||
		r.Response != "" || r.ResponseBytes != 0 || r.Status != record.StatusOK {
		t.Errorf("record 2 = %+v", r)
	}
}

func TestNotification(t *testing.T) {
	base, sink := start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}), 0)

	resp, body := post(t, base+"/mcp", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if resp.StatusCode != http.StatusAccepted || body != "" {
		t.Fatalf("client got %d %q", resp.StatusCode, body)
	}
	r := sink.wait(t, 1)[0]
	if r.Kind != record.KindMCP || r.RPCMethod != "notifications/initialized" || r.HTTPStatus != 202 ||
		r.Status != record.StatusOK || r.ResponseTruncated {
		t.Errorf("record = %+v", r)
	}
}

func TestErrors(t *testing.T) {
	long := strings.Repeat("é", 400) // 800 bytes
	isErrorResult := fmt.Sprintf(`{"content":[{"type":"text","text":"%s"}],"isError":true}`, long)
	tests := []struct {
		name         string
		status       int
		contentType  string
		body         string
		wantError    string
		wantResponse string
	}{
		{
			name:         "json-rpc error",
			status:       200,
			contentType:  "text/event-stream",
			body:         sse(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"Unknown tool"}}`),
			wantError:    "Unknown tool",
			wantResponse: `{"code":-32602,"message":"Unknown tool"}`,
		},
		{
			name:         "isError result",
			status:       200,
			contentType:  "text/event-stream",
			body:         sse(`{"jsonrpc":"2.0","id":1,"result":` + isErrorResult + `}`),
			wantError:    strings.Repeat("é", 250),
			wantResponse: isErrorResult,
		},
		{
			name:         "http error without json-rpc body",
			status:       500,
			contentType:  "text/plain",
			body:         "boom",
			wantResponse: "boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, sink := start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}), 0)

			resp, body := post(t, base+"/mcp", toolCall(1, `{}`))
			if resp.StatusCode != tt.status || body != tt.body {
				t.Fatalf("client got %d %q", resp.StatusCode, body)
			}
			r := sink.wait(t, 1)[0]
			if r.Status != record.StatusError || r.Error != tt.wantError || r.Response != tt.wantResponse ||
				r.HTTPStatus != tt.status || r.ResultCount != nil {
				t.Errorf("record = %+v", r)
			}
		})
	}
}

func TestEmptyResults(t *testing.T) {
	msg, _ := toolResult(1, `{"results":[],"total":0}`)
	base, sink := start(t, serve("text/event-stream", sse(msg)), 0)

	post(t, base+"/mcp", toolCall(1, `{"workspace":"w","query":"nothing"}`))
	r := sink.wait(t, 1)[0]
	if r.Status != record.StatusEmpty || count(t, r) != 0 || r.Error != "" {
		t.Errorf("record = %+v", r)
	}
}

func TestClientFromInitialize(t *testing.T) {
	msg, _ := toolResult(2, `{"results":[1]}`)
	base, sink := start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"initialize"`) {
			w.Header().Set("Mcp-Session-Id", "sess-1")
			io.WriteString(w, sse(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26"}}`))
			return
		}
		io.WriteString(w, sse(msg))
	}), 0)

	resp, _ := post(t, base+"/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"claude-code","version":"1.2.3"}}}`)
	if got := resp.Header.Get("Mcp-Session-Id"); got != "sess-1" {
		t.Fatalf("client Mcp-Session-Id = %q", got)
	}
	do(t, http.MethodPost, base+"/mcp", toolCall(2, `{}`), map[string]string{"Mcp-Session-Id": "sess-1"})
	do(t, http.MethodPost, base+"/mcp", toolCall(2, `{}`), map[string]string{"Mcp-Session-Id": "other"})

	recs := sink.wait(t, 3)
	if r := recs[0]; r.Kind != record.KindMCP || r.RPCMethod != "initialize" || r.SessionID != "sess-1" || r.Client != "claude-code/1.2.3" {
		t.Errorf("initialize record = %+v", r)
	}
	if r := recs[1]; r.Kind != record.KindMCPTool || r.SessionID != "sess-1" || r.Client != "claude-code/1.2.3" {
		t.Errorf("tool record = %+v", r)
	}
	if r := recs[2]; r.SessionID != "other" || r.Client != "" {
		t.Errorf("other-session record = %+v", r)
	}
}

func TestWorkspaceQueryFallback(t *testing.T) {
	msg, _ := toolResult(1, `ok`)
	base, sink := start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("workspace"); got != "proj" {
			t.Errorf("upstream workspace query = %q", got)
		}
		serve("text/event-stream", sse(msg))(w, r)
	}), 0)

	post(t, base+"/mcp?workspace=proj", toolCall(1, `{"query":"q"}`))
	post(t, base+"/mcp?workspace=proj", toolCall(1, `{"query":"q","workspace":"explicit"}`))
	post(t, base+"/mcp?workspace=proj", toolCall(1, `{"query":"q","workspace":7}`))

	recs := sink.wait(t, 3)
	for i, want := range []string{"proj", "explicit", "proj"} {
		if recs[i].Workspace != want {
			t.Errorf("record %d Workspace = %q, want %q", i, recs[i].Workspace, want)
		}
	}
	if recs[0].ResultCount != nil || recs[0].Status != record.StatusOK {
		t.Errorf("record = %+v", recs[0])
	}
}

func TestTruncation(t *testing.T) {
	const limit = 100

	t.Run("rest", func(t *testing.T) {
		full := strings.Repeat("0123456789", 5000)
		base, sink := start(t, serve("text/plain", full), limit)

		if _, body := do(t, http.MethodGet, base+"/api/v1/dump", "", nil); body != full {
			t.Fatalf("client got %d bytes, want %d", len(body), len(full))
		}
		r := sink.wait(t, 1)[0]
		if r.Response != full[:limit] || r.ResponseBytes != len(full) || !r.ResponseTruncated {
			t.Errorf("Response len %d, ResponseBytes %d, truncated %v", len(r.Response), r.ResponseBytes, r.ResponseTruncated)
		}
	})

	t.Run("mcp tool", func(t *testing.T) {
		msg, result := toolResult(1, `{"results":[`+strings.Repeat(`"0123456789",`, 4999)+`"x"]}`)
		respBody := sse(msg)
		base, sink := start(t, serve("text/event-stream", respBody), limit)

		if _, body := post(t, base+"/mcp", toolCall(1, `{}`)); body != respBody {
			t.Fatalf("client got %d bytes, want %d", len(body), len(respBody))
		}
		r := sink.wait(t, 1)[0]
		if r.Response != result[:limit] || r.ResponseBytes != len(result) || !r.ResponseTruncated {
			t.Errorf("Response len %d, ResponseBytes %d, truncated %v", len(r.Response), r.ResponseBytes, r.ResponseTruncated)
		}
		if count(t, r) != 5000 || r.Status != record.StatusOK {
			t.Errorf("ResultCount = %d, Status = %q", count(t, r), r.Status)
		}
	})
}

func TestREST(t *testing.T) {
	respBody := `{"results":[{"id":1},{"id":2}]}`
	base, sink := start(t, serve("application/json", respBody), 0)

	reqBody := `{"workspace":"w","query":"q"}`
	if _, body := post(t, base+"/api/v1/query", reqBody); body != respBody {
		t.Fatalf("client got %q", body)
	}
	do(t, http.MethodGet, base+"/api/v1/search?q=x&workspace=w2", "", nil)
	do(t, http.MethodGet, base+"/api/v1/status", "", nil)

	recs := sink.wait(t, 3)
	r := recs[0]
	if r.Kind != record.KindREST || r.HTTPMethod != "POST" || r.Path != "/api/v1/query" || string(r.Args) != reqBody ||
		r.Workspace != "w" || r.Response != respBody || r.ResponseBytes != len(respBody) || r.ResponseTruncated ||
		count(t, r) != 2 || r.Status != record.StatusOK || r.RPCMethod != "" || r.Tool != "" {
		t.Errorf("POST record = %+v", r)
	}
	if r := recs[1]; r.HTTPMethod != "GET" || string(r.Args) != `{"q":["x"],"workspace":["w2"]}` || r.Workspace != "w2" {
		t.Errorf("GET record = %+v", r)
	}
	if r := recs[2]; r.Args != nil || r.Workspace != "" {
		t.Errorf("GET without query record = %+v", r)
	}
}

func TestRESTStatus(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		base, sink := start(t, serve("application/json", `{"results":[]}`), 0)
		do(t, http.MethodGet, base+"/api/v1/search?q=x", "", nil)
		if r := sink.wait(t, 1)[0]; r.Status != record.StatusEmpty || count(t, r) != 0 {
			t.Errorf("record = %+v", r)
		}
	})
	t.Run("http error", func(t *testing.T) {
		base, sink := start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":"no such workspace"}`)
		}), 0)
		resp, _ := do(t, http.MethodGet, base+"/api/v1/search?q=x", "", nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("client status = %d", resp.StatusCode)
		}
		if r := sink.wait(t, 1)[0]; r.Status != record.StatusError || r.HTTPStatus != 404 || r.Response != `{"error":"no such workspace"}` {
			t.Errorf("record = %+v", r)
		}
	})
}

func TestStaticAssetNotStored(t *testing.T) {
	for _, contentType := range []string{"text/html; charset=utf-8", "text/javascript", "text/css", "image/png"} {
		t.Run(contentType, func(t *testing.T) {
			asset := "<html>" + strings.Repeat("x", 1000) + "</html>"
			base, sink := start(t, serve(contentType, asset), 0)

			if _, body := do(t, http.MethodGet, base+"/assets/app", "", nil); body != asset {
				t.Fatalf("client got %d bytes, want %d", len(body), len(asset))
			}
			r := sink.wait(t, 1)[0]
			if r.Kind != record.KindREST || r.Response != "" || r.ResponseBytes != len(asset) || r.ResponseTruncated ||
				r.Status != record.StatusOK || r.ResultCount != nil {
				t.Errorf("record = %+v", r)
			}
		})
	}
}

func TestEncodedResponseNotStored(t *testing.T) {
	blob := "\x1f\x8b not really gzip"
	base, sink := start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		io.WriteString(w, blob)
	}), 0)

	// DisableCompression makes the test client hand back the raw bytes.
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	t.Cleanup(client.CloseIdleConnections)
	resp, err := client.Get(base + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != blob || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("client got %q, Content-Encoding %q", body, resp.Header.Get("Content-Encoding"))
	}
	if r := sink.wait(t, 1)[0]; r.Response != "" || r.ResponseBytes != len(blob) {
		t.Errorf("record = %+v", r)
	}
}

func TestStreamingNotBuffered(t *testing.T) {
	msg, result := toolResult(1, `{"results":[1]}`)
	first, second := sse(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":1}}`), sse(msg)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()

	base, sink := start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, first)
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, second)
	}), 0)

	resp, err := http.Post(base+"/mcp", "application/json", strings.NewReader(toolCall(1, `{}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	type read struct {
		data string
		err  error
	}
	got := make(chan read, 1)
	go func() {
		buf := make([]byte, len(first))
		_, err := io.ReadFull(resp.Body, buf)
		got <- read{string(buf), err}
	}()
	select {
	case r := <-got:
		if r.err != nil || r.data != first {
			t.Fatalf("first event = %q, %v", r.data, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first event did not reach the client while the upstream was still streaming")
	}
	if n := len(sink.wait(t, 0)); n != 0 {
		t.Fatalf("%d records before the stream ended", n)
	}

	unblock()
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(rest) != second {
		t.Fatalf("rest of stream = %q, %v", rest, err)
	}
	if r := sink.wait(t, 1)[0]; r.Response != result || count(t, r) != 1 {
		t.Errorf("record = %+v", r)
	}
}

func TestEventStreamGETNotRecorded(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()

	base, sink := start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/status" {
			serve("application/json", `{"ok":true}`)(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: endpoint\ndata: /sse?sessionId=1\n\n")
		w.(http.Flusher).Flush()
		<-release
	}), 0)

	for _, path := range []string{"/sse", "/mcp"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(resp.Body).ReadString('\n')
		if err != nil || line != "event: endpoint\n" {
			t.Fatalf("GET %s first line = %q, %v", path, line, err)
		}
		defer resp.Body.Close()
	}
	unblock()

	do(t, http.MethodGet, base+"/api/v1/status", "", nil)
	if r := sink.wait(t, 1)[0]; r.Path != "/api/v1/status" {
		t.Errorf("record = %+v", r)
	}
}

func TestLegacySSEPost(t *testing.T) {
	base, sink := start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}), 0)

	post(t, base+"/sse?sessionId=1", toolCall(1, `{"workspace":"w"}`))
	r := sink.wait(t, 1)[0]
	if r.Kind != record.KindMCPTool || r.Path != "/sse" || r.Tool != "memory_query" || r.Workspace != "w" || r.Response != "" {
		t.Errorf("record = %+v", r)
	}
}

func TestUpstreamDown(t *testing.T) {
	up := httptest.NewServer(http.NotFoundHandler())
	upstreamURL := up.URL
	up.Close()
	base, sink := startProxy(t, upstreamURL, &memSink{}, 0)

	resp, _ := post(t, base+"/mcp", toolCall(1, `{"workspace":"w"}`))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("client status = %d, want 502", resp.StatusCode)
	}
	resp, _ = do(t, http.MethodGet, base+"/api/v1/status", "", nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("client status = %d, want 502", resp.StatusCode)
	}

	recs := sink.wait(t, 2)
	if r := recs[0]; r.Kind != record.KindMCPTool || r.Tool != "memory_query" || r.Workspace != "w" ||
		r.Status != record.StatusError || r.HTTPStatus != 502 || r.Error == "" {
		t.Errorf("mcp record = %+v", r)
	}
	if r := recs[1]; r.Kind != record.KindREST || r.Status != record.StatusError || r.HTTPStatus != 502 || r.Error == "" {
		t.Errorf("rest record = %+v", r)
	}
}

func TestFailingSink(t *testing.T) {
	msg, _ := toolResult(1, `{"results":[1]}`)
	respBody := sse(msg)
	up := httptest.NewServer(serve("text/event-stream", respBody))
	t.Cleanup(up.Close)
	base, sink := startProxy(t, up.URL, &memSink{err: errors.New("disk full")}, 0)

	for range 2 {
		resp, body := post(t, base+"/mcp", toolCall(1, `{}`))
		if resp.StatusCode != 200 || body != respBody {
			t.Fatalf("client got %d %q", resp.StatusCode, body)
		}
	}
	sink.wait(t, 2)
}

func TestNullErrorIsNotFailure(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"results\":[1]}"}]},"error":null}`
	base, sink := start(t, serve("application/json", body), 0)

	post(t, base+"/mcp", toolCall(1, `{"workspace":"w","query":"q"}`))
	r := sink.wait(t, 1)[0]
	if r.Status != record.StatusOK || count(t, r) != 1 || r.Error != "" {
		t.Errorf("record = %+v", r)
	}
}

func TestLargeRequestBody(t *testing.T) {
	var got int
	msg, _ := toolResult(1, `{"results":[1]}`)
	base, sink := start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = len(b)
		serve("text/event-stream", sse(msg))(w, r)
	}), 0)

	req := toolCall(1, `{"workspace":"w","content":"`+strings.Repeat("x", 2<<20)+`"}`)
	post(t, base+"/mcp", req)
	r := sink.wait(t, 1)[0]
	if got != len(req) {
		t.Errorf("upstream received %d bytes, want %d", got, len(req))
	}
	if r.Kind != record.KindMCPTool || r.Tool != "memory_query" || r.Workspace != "w" {
		t.Errorf("record kind=%q tool=%q workspace=%q", r.Kind, r.Tool, r.Workspace)
	}
}
