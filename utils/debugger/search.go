// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ebitenui/ebitenui/image"
	"github.com/ebitenui/ebitenui/widget"
	"github.com/user-none/erings/internal/debugserver/responses"
	"github.com/user-none/erings/utils/debugger/client"
	"github.com/user-none/erings/utils/debugger/ui"
)

// searchPollTicks is the candidate list refresh cadence while a search
// is active, in 60Hz ticks.
const searchPollTicks = 30

// searchListCount is how many candidates the panel fetches.
const searchListCount = 50

// searchListRows is the candidate list's fixed visible height in text
// rows; the rest scrolls.
const searchListRows = 10

// searchPanel is the memory-search panel state. active comes from the
// periodic state response; the candidate rows come from the list
// command, paged by searchListCount.
type searchPanel struct {
	rows     *widget.Container
	status   *widget.Text
	valInput *widget.TextInput
	prevBtn  *widget.Button
	nextBtn  *widget.Button

	// page is the zero-based candidate page. Any search-mutating
	// command resets it; a page emptied by filtering snaps back to the
	// last populated one.
	page int

	active bool

	// One list request is outstanding at a time; a forced refresh
	// arriving mid-flight queues exactly one follow-up.
	inFlight   bool
	listQueued bool
}

// buildSearchPanel creates the search controls and the candidate list.
// Every control maps directly onto a server command; region-scoped
// baselines stay on the command line.
func (a *app) buildSearchPanel() *widget.Container {
	panel := widget.NewContainer(
		widget.ContainerOpts.BackgroundImage(image.NewNineSliceColor(ui.Surface)),
		widget.ContainerOpts.Layout(widget.NewGridLayout(
			widget.GridLayoutOpts.Columns(1),
			widget.GridLayoutOpts.Stretch([]bool{true}, []bool{false, false, true, false}),
			widget.GridLayoutOpts.Padding(widget.NewInsetsSimple(ui.Px(6))),
			widget.GridLayoutOpts.Spacing(0, ui.Px(6)),
		)),
	)

	controlItems := []widget.PreferredSizeLocateableWidget{
		ui.Label("Search", ui.TextSecondary),
	}
	for _, w := range []string{"8", "16", "32"} {
		width := w
		controlItems = append(controlItems, ui.Button("w"+width, func(args *widget.ButtonClickedEventArgs) {
			a.searchCommand("width " + width)
		}))
	}
	controlItems = append(controlItems,
		ui.PrimaryButton("Baseline", func(args *widget.ButtonClickedEventArgs) {
			a.searchCommand("baseline")
		}),
		ui.Button("Rebase", func(args *widget.ButtonClickedEventArgs) {
			a.searchCommand("rebase")
		}),
		ui.Button("Reset", func(args *widget.ButtonClickedEventArgs) {
			a.searchCommand("reset")
		}),
	)
	panel.AddChild(ui.HRow(ui.Px(4), controlItems...))

	a.search.valInput = ui.TextInput("value", 90)
	a.inputs.Add(a.search.valInput)
	var filterItems []widget.PreferredSizeLocateableWidget
	for _, op := range []string{"dec", "inc", "same", "diff"} {
		name := op
		filterItems = append(filterItems, ui.Button(name, func(args *widget.ButtonClickedEventArgs) {
			a.searchCommand("filter " + name)
		}))
	}
	filterItems = append(filterItems, a.search.valInput)
	for _, op := range []string{"eq", "ne", "lt", "gt"} {
		name := op
		filterItems = append(filterItems, ui.Button(name, func(args *widget.ButtonClickedEventArgs) {
			v := strings.TrimSpace(a.search.valInput.GetText())
			if v == "" {
				a.logf("filter %s needs a value", name)
				return
			}
			a.searchCommand(fmt.Sprintf("filter %s %s", name, v))
		}))
	}
	panel.AddChild(ui.HRow(ui.Px(4), filterItems...))

	a.search.rows = widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewRowLayout(
			widget.RowLayoutOpts.Direction(widget.DirectionVertical),
			widget.RowLayoutOpts.Spacing(ui.Px(2)),
		)),
	)
	view, wrap := ui.Scrollable(a.search.rows, ui.Surface)
	// The candidate list holds a fixed design height so the middle
	// section's total height is deterministic; window growth goes to
	// the log instead.
	view.MinHeight = ui.TextRowsHeight(searchListRows)
	view.MaxHeight = view.MinHeight
	panel.AddChild(wrap)

	// Page controls and the candidate count sit under the list. The
	// list refresh owns the label and button states while a search is
	// active; setSearchState resets them when the search ends.
	a.search.prevBtn = ui.Button("<", func(args *widget.ButtonClickedEventArgs) {
		if a.search.page > 0 {
			a.search.page--
			a.pollCandidates(true)
		}
	})
	a.search.nextBtn = ui.Button(">", func(args *widget.ButtonClickedEventArgs) {
		a.search.page++
		a.pollCandidates(true)
	})
	a.search.prevBtn.GetWidget().Disabled = true
	a.search.nextBtn.GetWidget().Disabled = true
	a.search.status = ui.Label("no search", ui.TextSecondary)
	panel.AddChild(ui.HRow(ui.Px(4), a.search.prevBtn, a.search.nextBtn, a.search.status))

	return panel
}

// searchCommand sends one search-affecting command, logs its response,
// and refreshes the state and candidate list. The candidate set just
// changed, so paging restarts at the first page.
func (a *app) searchCommand(line string) {
	a.search.page = 0
	a.send(line, func(r client.Response) {
		if out := formatResponse(r); out != "" {
			a.logf("%s", out)
		}
		a.pollState()
		a.pollCandidates(true)
	})
}

// pollSearch refreshes the candidate list on its cadence while a
// search is active.
func (a *app) pollSearch() {
	if !a.search.active || a.tick%searchPollTicks != 0 {
		return
	}
	a.pollCandidates(false)
}

// pollCandidates re-reads the current candidate page. A successful
// response owns the count label and page-button states; an error (no
// search active) clears the rows and leaves them to setSearchState.
// force asks for a fresh read even mid-flight: the current response
// still lands, and one follow-up read runs right after it.
func (a *app) pollCandidates(force bool) {
	if !a.connected {
		return
	}
	if a.search.inFlight {
		if force {
			a.search.listQueued = true
		}
		return
	}
	a.search.inFlight = true
	a.send(fmt.Sprintf("list %d %d", searchListCount, a.search.page*searchListCount),
		func(r client.Response) {
			a.search.inFlight = false
			refetch := a.search.listQueued
			a.search.listQueued = false
			if r.Err != nil {
				a.rebuildCandidateRows(responses.CandidateList{})
			} else {
				var cl responses.CandidateList
				if json.Unmarshal(r.Data, &cl) == nil {
					// A page emptied from under us (the set shrank)
					// snaps back to the last populated page and
					// refetches.
					if len(cl.Candidates) == 0 && cl.Total > 0 && a.search.page > 0 {
						a.search.page = (cl.Total - 1) / searchListCount
						refetch = true
					} else {
						a.rebuildCandidateRows(cl)
						a.updateSearchFooter(cl)
					}
				}
			}
			if refetch {
				a.pollCandidates(true)
			}
		})
}

// updateSearchFooter renders the count label and page-button states
// from one list response.
func (a *app) updateSearchFooter(cl responses.CandidateList) {
	if a.search.status == nil {
		return
	}
	if cl.Total == 0 {
		a.search.status.Label = "0 candidates"
	} else {
		a.search.status.Label = fmt.Sprintf("%d-%d of %d",
			cl.Offset+1, cl.Offset+len(cl.Candidates), cl.Total)
	}
	a.search.prevBtn.GetWidget().Disabled = a.search.page == 0
	a.search.nextBtn.GetWidget().Disabled = cl.Offset+len(cl.Candidates) >= cl.Total
}

// setSearchState updates the panel from the periodic state response.
// The count label under the list belongs to the list refresh while a
// search is active; this only handles the transitions.
func (a *app) setSearchState(active bool, total int) {
	wasActive := a.search.active
	a.search.active = active
	if !active {
		a.search.page = 0
		if a.search.status != nil {
			a.search.status.Label = "no search"
			a.search.prevBtn.GetWidget().Disabled = true
			a.search.nextBtn.GetWidget().Disabled = true
		}
		if wasActive {
			a.rebuildCandidateRows(responses.CandidateList{})
		}
		return
	}
	// A search that appeared outside a panel action (command line, or
	// one surviving from a previous client) still gets its list.
	if !wasActive {
		a.pollCandidates(true)
	}
}

// rebuildCandidateRows fills the candidate list. Clicking a row jumps
// the memory view to that address.
func (a *app) rebuildCandidateRows(cl responses.CandidateList) {
	c := a.search.rows
	if c == nil {
		return
	}
	c.RemoveChildren()
	if len(cl.Candidates) == 0 {
		c.AddChild(ui.Label("(none)", ui.TextSecondary))
		return
	}
	digits := cl.Width / 4
	for _, cand := range cl.Candidates {
		addr := cand.Addr
		label := fmt.Sprintf("0x%08X  cur=%d (0x%0*X)  base=%d (0x%0*X)",
			cand.Addr, cand.Cur, digits, cand.Cur, cand.Base, digits, cand.Base)
		c.AddChild(ui.LinkButton(label, ui.Text, func(args *widget.ButtonClickedEventArgs) {
			a.jumpTo(addr)
		}))
	}
}
