// Package record defines the log record shared by the proxy (producer) and
// the sink (writer). It is the contract between the two; neither imports the
// other.
package record

import (
	"encoding/json"
	"time"
)

const (
	KindMCPTool = "mcp_tool" // JSON-RPC tools/call over /mcp
	KindMCP     = "mcp"      // any other JSON-RPC method over /mcp or /sse
	KindREST    = "rest"     // any non-MCP HTTP request (e.g. /api/v1/query)

	StatusOK    = "ok"
	StatusEmpty = "empty" // succeeded but returned no results
	StatusError = "error" // HTTP >= 400, JSON-RPC error, or tool result isError
)

// Record is one observed call, written as one JSON line.
type Record struct {
	Time      time.Time `json:"ts"`
	Kind      string    `json:"kind"`
	Client    string    `json:"client,omitempty"`     // MCP clientInfo "name/version", else the first User-Agent token
	UserAgent string    `json:"user_agent,omitempty"` // HTTP User-Agent
	SessionID string    `json:"session_id,omitempty"` // Mcp-Session-Id header when present
	Workspace string    `json:"workspace,omitempty"`  // arguments.workspace, else ?workspace= query

	HTTPMethod string `json:"http_method"`
	Path       string `json:"path"`
	RPCMethod  string `json:"rpc_method,omitempty"` // e.g. "tools/call"
	Tool       string `json:"tool,omitempty"`       // e.g. "memory_query"

	Args json.RawMessage `json:"args,omitempty"` // tool arguments, or REST request body/query

	HTTPStatus int    `json:"http_status"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	LatencyMs  int64  `json:"latency_ms"`

	ResultCount       *int   `json:"result_count,omitempty"` // nil when not derivable
	ResponseBytes     int    `json:"response_bytes"`         // full size before truncation
	Response          string `json:"response,omitempty"`     // full response payload, capped
	ResponseTruncated bool   `json:"response_truncated,omitempty"`
}

// Sink persists records. Implementations must be safe for concurrent use.
// Write must never block the proxied request for long; a failing sink must
// not fail the request.
type Sink interface {
	Write(Record) error
	Close() error
}
