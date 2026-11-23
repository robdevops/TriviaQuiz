package main

import (
	"encoding/json"
	"time"

	"github.com/robdevops/signalbot/addon"
)

// The protocol as the tests, playing the bot, read and write it. The
// server's side is github.com/robdevops/signalbot/addon.
type (
	callContext   = addon.Context
	toolResult    = addon.Result
	outboxPage    = addon.Page
	outboxOp      = addon.Op
	sampleRequest = addon.SampleRequest
	sampleResult  = addon.SampleResult
)

const (
	protocolVersion = addon.Protocol
	vendor          = addon.Vendor
	outboxURI       = addon.OutboxURI
)

// The addon package's limits: how long an op unread is kept, and the
// longest line it reads.
const (
	keepOps = time.Hour
	maxLine = 1 << 20
)

// JSON-RPC's error codes, and MCP's for a resource that isn't there.
const (
	codeNoMethod      = -32601
	codeInvalidParams = -32602
	codeNotFound      = -32002
)

// response is the answer to one request: its result or an error.
type response struct {
	ID     json.RawMessage
	Result any
	Error  *rpcError
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type (
	initializeResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Experimental map[string]addon.Capability `json:"experimental"`
			Resources    struct {
				Subscribe bool `json:"subscribe"`
			} `json:"resources"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	tool struct {
		Name        string `json:"name"`
		InputSchema struct {
			Type string `json:"type"`
		} `json:"inputSchema"`
		Meta struct {
			Visibility []string `json:"io.github.robdevops.signalbot/visibility"`
		} `json:"_meta"`
	}
	readResult struct {
		Contents []struct {
			URI      string `json:"uri"`
			MimeType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"contents"`
	}
	uriParams struct {
		URI string `json:"uri"`
	}
)

// textContent is a block of text.
func textContent(s string) addon.Content { return addon.Content{Type: "text", Text: s} }
