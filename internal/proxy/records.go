package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/nano-step/nano-brain-observe/internal/record"
)

const maxErrorLen = 500

// rpcMessage is a JSON-RPC request (Method set) or response (Result/Error).
type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// key identifies the message for request/response matching; "" for a
// notification.
func (m rpcMessage) key() string {
	id := string(bytes.TrimSpace(m.ID))
	if id == "null" {
		return ""
	}
	return id
}

// parseRPC decodes a single JSON-RPC message or a batch; nil if data is
// neither.
func parseRPC(data []byte) []rpcMessage {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	if data[0] == '[' {
		var batch []rpcMessage
		if json.Unmarshal(data, &batch) != nil {
			return nil
		}
		return batch
	}
	var m rpcMessage
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return []rpcMessage{m}
}

// sseData returns the data payload of each event in an SSE stream. A final
// event cut off by truncation is returned as is.
func sseData(stream []byte) [][]byte {
	var events [][]byte
	var cur []byte
	for _, line := range bytes.Split(stream, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 {
			if cur != nil {
				events = append(events, cur)
				cur = nil
			}
			continue
		}
		data, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			continue
		}
		if cur != nil {
			cur = append(cur, '\n')
		}
		cur = append(cur, bytes.TrimPrefix(data, []byte(" "))...)
	}
	if cur != nil {
		events = append(events, cur)
	}
	return events
}

// countFields are the array members nano-brain tools return their hits in.
// "tags" is deliberately absent: memory_get returns a document whose own
// (often empty) tags array is not a result list.
var countFields = []string{"results", "symbols", "edges", "impacted", "chain", "workspaces", "nodes", "items", "documents"}

// countResults derives how many results a JSON payload carries: the length
// of a top-level array, or of the first countFields member that is an array.
// An object with "found": false (memory_flow, memory_flowchart) counts as 0.
// It returns nil when the payload has none of these.
func countResults(data []byte) *int {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	arrayLen := func(raw json.RawMessage) *int {
		// The '[' check keeps null from decoding into an empty slice.
		var items []json.RawMessage
		if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &items) != nil {
			return nil
		}
		n := len(items)
		return &n
	}
	if data[0] == '[' {
		return arrayLen(data)
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return nil
	}
	for _, f := range countFields {
		if n := arrayLen(bytes.TrimSpace(obj[f])); n != nil {
			return n
		}
	}
	if string(bytes.TrimSpace(obj["found"])) == "false" {
		zero := 0
		return &zero
	}
	return nil
}

// toolText returns the first text content of a tools/call result and its
// isError flag.
func toolText(result json.RawMessage) (text string, isError bool) {
	var r struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	_ = json.Unmarshal(result, &r)
	for _, c := range r.Content {
		if c.Type == "text" {
			return c.Text, r.IsError
		}
	}
	return "", r.IsError
}

// workspaceOf returns the string member "workspace" of a JSON object, or "".
func workspaceOf(args json.RawMessage) string {
	var a struct {
		Workspace json.RawMessage `json:"workspace"`
	}
	var ws string
	if json.Unmarshal(args, &a) != nil || json.Unmarshal(a.Workspace, &ws) != nil {
		return ""
	}
	return ws
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// setResponse stores data, capped, as the record's response; total is its
// size before any truncation.
func (c *call) setResponse(rec *record.Record, data []byte, total int) {
	rec.Response = truncate(string(data), c.h.maxResp)
	rec.ResponseBytes = total
	rec.ResponseTruncated = total > len(rec.Response)
}

// records builds the log records for a finished call.
func (c *call) records(latency time.Duration) []record.Record {
	sessionID := c.req.Header.Get(sessionHdr)
	if sessionID == "" {
		sessionID = c.header.Get(sessionHdr)
	}
	base := record.Record{
		Time:       c.start,
		UserAgent:  c.req.UserAgent(),
		SessionID:  sessionID,
		HTTPMethod: c.req.Method,
		Path:       c.req.URL.Path,
		HTTPStatus: c.status,
		Status:     record.StatusOK,
		LatencyMs:  latency.Milliseconds(),
	}
	if sessionID != "" {
		base.Client = c.h.client(sessionID)
	}
	if c.status >= 400 {
		base.Status = record.StatusError
	}
	if c.err != nil {
		base.Error = c.err.Error()
	}
	if len(c.rpcs) == 0 {
		return []record.Record{c.restRecord(base)}
	}
	return c.rpcRecords(base)
}

func (c *call) restRecord(rec record.Record) record.Record {
	query := c.req.URL.Query()
	rec.Kind = record.KindREST
	switch {
	case len(c.body) > 0 && json.Valid(c.body):
		rec.Args = c.body
	case len(query) > 0:
		rec.Args, _ = json.Marshal(query)
	}
	if rec.Workspace = workspaceOf(c.body); rec.Workspace == "" {
		rec.Workspace = query.Get("workspace")
	}
	if !c.logBody {
		rec.ResponseBytes = c.total
		return rec
	}
	c.setResponse(&rec, c.captured, c.total)
	if isJSON(c.mediaType) && len(c.captured) == c.total {
		rec.ResultCount = countResults(c.captured)
		if rec.ResultCount != nil && *rec.ResultCount == 0 && rec.Status == record.StatusOK {
			rec.Status = record.StatusEmpty
		}
	}
	return rec
}

func (c *call) rpcRecords(base record.Record) []record.Record {
	payloads := [][]byte{c.captured}
	if c.mediaType == "text/event-stream" {
		payloads = sseData(c.captured)
	}
	responses := map[string]rpcMessage{}
	for _, p := range payloads {
		for _, m := range parseRPC(p) {
			if k := m.key(); k != "" && m.Method == "" {
				responses[k] = m
			}
		}
	}

	recs := make([]record.Record, 0, len(c.rpcs))
	for _, req := range c.rpcs {
		rec := base
		rec.Kind = record.KindMCP
		rec.RPCMethod = req.Method
		rec.Args = req.Params
		switch req.Method {
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			rec.Kind = record.KindMCPTool
			rec.Tool = p.Name
			rec.Args = p.Arguments
		case "initialize":
			var p struct {
				ClientInfo struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"clientInfo"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.ClientInfo.Name != "" {
				base.Client = strings.TrimSuffix(p.ClientInfo.Name+"/"+p.ClientInfo.Version, "/")
				rec.Client = base.Client
				if base.SessionID != "" {
					c.h.rememberClient(base.SessionID, base.Client)
				}
			}
		}
		if rec.Workspace = workspaceOf(rec.Args); rec.Workspace == "" {
			rec.Workspace = c.req.URL.Query().Get("workspace")
		}

		resp, matched := responses[req.key()]
		switch {
		case matched && len(resp.Error) > 0 && string(resp.Error) != "null":
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(resp.Error, &e)
			rec.Status = record.StatusError
			rec.Error = truncate(e.Message, maxErrorLen)
			c.setResponse(&rec, resp.Error, len(resp.Error))
		case matched:
			text, isError := toolText(resp.Result)
			if isError {
				rec.Status = record.StatusError
				rec.Error = truncate(text, maxErrorLen)
			} else if rec.ResultCount = countResults([]byte(text)); rec.ResultCount != nil &&
				*rec.ResultCount == 0 && rec.Status == record.StatusOK {
				rec.Status = record.StatusEmpty
			}
			c.setResponse(&rec, resp.Result, len(resp.Result))
		case len(c.rpcs) == 1 && c.logBody:
			// No JSON-RPC response to pick apart (plain error body or
			// oversized response): keep the raw body.
			c.setResponse(&rec, c.captured, c.total)
		case len(c.rpcs) == 1:
			rec.ResponseBytes = c.total
		}
		recs = append(recs, rec)
	}
	return recs
}
