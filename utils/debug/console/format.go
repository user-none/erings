// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package console

import (
	"encoding/json"
	"fmt"
)

// result is a command response. Every handler returns one. Text mode
// sends text() to the client. JSON mode marshals the result as the data
// field of a {"type":"resp","data":...} envelope, so structured results
// carry json tags and plain messages marshal as strings.
type result interface {
	text() string
}

// msg is a plain-message result. It renders as itself in text mode and
// as a JSON string in JSON mode.
type msg string

func (m msg) text() string { return string(m) }

// respEnvelope wraps a successful command response in JSON mode. The
// client matches resp lines to sent commands in order: the connection
// executes commands one at a time and every command produces exactly
// one resp or error line.
type respEnvelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// errorEnvelope wraps a command error in JSON mode.
type errorEnvelope struct {
	Type string `json:"type"`
	Msg  string `json:"msg"`
}

// watchEvent is a pushed watch change in JSON mode.
type watchEvent struct {
	Type  string `json:"type"`
	Frame uint64 `json:"frame"`
	Addr  uint32 `json:"addr"`
	Width int    `json:"width"`
	Prev  uint32 `json:"prev"`
	Cur   uint32 `json:"cur"`
}

// breakEvent is a pushed break fire in JSON mode. A break fire always
// pauses emulation.
type breakEvent struct {
	Type  string `json:"type"`
	Frame uint64 `json:"frame"`
	Addr  uint32 `json:"addr"`
	Width int    `json:"width"`
	Prev  uint32 `json:"prev"`
	Cur   uint32 `json:"cur"`
	Cond  string `json:"cond"`
}

// marshalLine renders v as a single JSON line. The envelope and event
// types contain only marshal-safe fields, so a marshal error cannot
// happen in practice; the fallback keeps the output stream valid JSON
// if one ever does.
func marshalLine(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"type":"error","msg":"marshal failed"}`
	}
	return string(b)
}

// formatResp renders a successful command result for the current output
// mode. In text mode an empty message stays empty, which the connection
// treats as a silent command. In JSON mode every command produces a
// line so the client's in-order response matching never skips.
func (c *Console) formatResp(r result) string {
	if c.jsonMode {
		return marshalLine(respEnvelope{Type: "resp", Data: r})
	}
	return r.text()
}

// formatErr renders a command error for the current output mode.
func (c *Console) formatErr(err error) string {
	if c.jsonMode {
		return marshalLine(errorEnvelope{Type: "error", Msg: err.Error()})
	}
	return "error: " + err.Error()
}

// cmdMode switches the output format. The response to the mode command
// itself is emitted in the newly selected mode, so a client that sends
// "mode json" sees JSON from that response onward. JSON mode also
// suppresses the interactive prompt. The mode resets to text whenever a
// client attaches.
func cmdMode(c *Console, args []string) (result, error) {
	if len(args) != 1 || (args[0] != "text" && args[0] != "json") {
		return nil, fmt.Errorf("usage: mode text|json")
	}
	c.jsonMode = args[0] == "json"
	return msg("mode " + args[0]), nil
}
