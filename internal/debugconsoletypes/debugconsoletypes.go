// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

// Package debugconsoletypes holds the shared data types of the debug
// console's JSON responses: the console server serializes them as the
// data field of its response envelopes and the debugger client decodes
// them. Types used by only one side (and the envelope and event line
// shapes, which each side models itself) stay out; this package is the
// shared vocabulary, nothing more.
package debugconsoletypes

// StateResult is the state command response. Width is the search value
// width setting, which applies whether or not a search is active.
type StateResult struct {
	Paused       bool   `json:"paused"`
	Frame        uint64 `json:"frame"`
	Width        int    `json:"width"`
	SearchActive bool   `json:"search_active"`
	Candidates   int    `json:"candidates"`
}

// ReadResult is the read command response. Data is the bytes hex
// encoded.
type ReadResult struct {
	Addr uint32 `json:"addr"`
	Data string `json:"data"`
}

// RegionList is the regions command response.
type RegionList struct {
	Regions []RegionInfo `json:"regions"`
}

// RegionInfo is one region list entry. Start and End bound the
// canonical range inclusive; Size is in bytes. Window is the full bus
// decode window the region mirrors through (Window == Size when not
// mirrored), so a client can fold mirror spellings the way the console
// does.
type RegionInfo struct {
	Name   string `json:"name"`
	Start  uint32 `json:"start"`
	End    uint32 `json:"end"`
	Size   uint32 `json:"size"`
	Window uint32 `json:"window"`
}

// WatchList is the watch list response.
type WatchList struct {
	Watches []WatchInfo `json:"watches"`
}

// WatchInfo is one watch list entry. Value is the last value read;
// Valid is false until the first between-frames read has seeded it.
type WatchInfo struct {
	Addr  uint32 `json:"addr"`
	Width int    `json:"width"`
	Value uint32 `json:"value"`
	Valid bool   `json:"valid"`
}

// BreakList is the break list response.
type BreakList struct {
	Breaks []BreakInfo `json:"breaks"`
}

// BreakInfo is one break list entry. Val is meaningful only when
// HasVal is set (comparison operators).
type BreakInfo struct {
	Addr   uint32 `json:"addr"`
	Width  int    `json:"width"`
	Op     string `json:"op"`
	Val    uint32 `json:"val"`
	HasVal bool   `json:"has_val"`
}

// CandidateList is the list command response. Candidates holds up to
// the requested count in address order starting at Offset; Total is
// the full surviving count.
type CandidateList struct {
	Width      int             `json:"width"`
	Total      int             `json:"total"`
	Offset     int             `json:"offset"`
	Candidates []CandidateInfo `json:"candidates"`
}

// CandidateInfo is one surviving search candidate. Cur is the live
// value at list time and Base is the current comparison baseline.
type CandidateInfo struct {
	Addr uint32 `json:"addr"`
	Cur  uint32 `json:"cur"`
	Base uint32 `json:"base"`
}
