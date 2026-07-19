// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package console

import (
	"encoding/json"
	"fmt"

	"github.com/user-none/erings/internal/debugconsoletypes"
)

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

// formatResp renders a successful command result for the current
// output mode. Handlers return either a plain message string or one of
// the structured response types. In text mode an empty message stays
// empty, which the connection treats as a silent command. In JSON mode
// every command produces a line so the client's in-order response
// matching never skips.
func (c *Console) formatResp(v any) string {
	if c.jsonMode {
		return marshalLine(respEnvelope{Type: "resp", Data: v})
	}
	return renderText(v)
}

// renderText maps a command result to its text-mode rendering. Every
// type a handler can return has a case; the tests exercise every
// command in both modes, so a missing case surfaces as the JSON
// fallback in a text-mode test expectation.
func renderText(v any) string {
	switch r := v.(type) {
	case string:
		return r
	case debugconsoletypes.StateResult:
		return stateText(r)
	case debugconsoletypes.ReadResult:
		return readText(r)
	case debugconsoletypes.RegionList:
		return regionListText(r)
	case debugconsoletypes.WatchList:
		return watchListText(r)
	case debugconsoletypes.BreakList:
		return breakListText(r)
	case debugconsoletypes.CandidateList:
		return candidateListText(r)
	case snapshotList:
		return snapshotListText(r)
	}
	return marshalLine(v)
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
func cmdMode(c *Console, args []string) (any, error) {
	if len(args) != 1 || (args[0] != "text" && args[0] != "json") {
		return nil, fmt.Errorf("usage: mode text|json")
	}
	c.jsonMode = args[0] == "json"
	return "mode " + args[0], nil
}
