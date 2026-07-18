package mcp

import (
	"encoding/json"
	"fmt"
)

// jsonRPCVersion is the only version pluto speaks.
const jsonRPCVersion = "2.0"

// rpcOutgoing is a JSON-RPC request (ID set) or notification (ID nil) pluto
// sends to a server.
type rpcOutgoing struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcIncoming is any JSON-RPC message a server sends back: a response (ID +
// result/error) or a server-initiated request/notification (Method set), which
// pluto logs and ignores since it advertises no client capabilities.
type rpcIncoming struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if len(e.Data) > 0 {
		return fmt.Sprintf("rpc error %d: %s (%s)", e.Code, e.Message, string(e.Data))
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// isResponse reports whether the message answers a request (has an id and no
// method), as opposed to a server-initiated request or notification.
func (m rpcIncoming) isResponse() bool {
	return m.ID != nil && m.Method == ""
}

// marshalRequest builds a JSON-RPC request frame with the given id.
func marshalRequest(id int64, method string, params any) ([]byte, error) {
	p, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(rpcOutgoing{JSONRPC: jsonRPCVersion, ID: &id, Method: method, Params: p})
}

// marshalNotification builds a JSON-RPC notification frame (no id).
func marshalNotification(method string, params any) ([]byte, error) {
	p, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(rpcOutgoing{JSONRPC: jsonRPCVersion, Method: method, Params: p})
}

// marshalParams renders params to raw JSON, treating nil as an omitted field.
func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		return raw, nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal params: %w", err)
	}
	return b, nil
}
