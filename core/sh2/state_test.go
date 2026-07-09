// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package sh2

import (
	"reflect"
	"testing"
)

// fillCPUState sets every State-captured CPU field to a distinct
// non-zero value (bools to true) so the zero-leaf walk in
// TestStateCapturesEverything can detect a State() copy that was
// never wired.
func fillCPUState(c *CPU) {
	for i := range c.reg.R {
		c.reg.R[i] = 0x1000 + uint32(i)
	}
	c.reg.PC = 0x06000100
	c.reg.PR = 0x06000200
	c.reg.SR = 0x000003F3
	c.reg.GBR = 0x06000300
	c.reg.VBR = 0x06000400
	c.reg.MACH = 0x12345678
	c.reg.MACL = 0x9ABCDEF0
	c.cycles = 987654321
	c.halted = true
	c.irl.Store(0x000E0047)

	c.prevPC = 0x060000FE
	c.delayPC = 0x06000102
	c.inDelay = true
	c.nmiPending = true
	c.nmiReq.Store(true)
	c.intInhibit = true
	c.pendingOp = 3
	c.pendingStep = 2
	c.pendingCount = 5
	c.pendingN = 7
	c.pendingAddr = 0x06001000
	c.pendingVal = 0x11111111
	c.pendingVal2 = 0x22222222
	c.pendingImm = 0x33333333
	c.lastLoadReg = 9
	c.deferredOp = 0x600C
	c.hasDeferred = true
	c.loadUseStall = true
	c.multiplierBusyUntil = 111222333
	c.busStall = 17
	c.nextPeripheralEvent = 444555666
	c.fetchLineAddr = 0x06000110
	c.fetchLineWay = 2
	c.fetchLineOff = 0x30
	c.sbycr = 0x5F
	c.bcr1 = 0x03F1

	c.ccr = 0x11
	for i := range c.cacheData {
		c.cacheData[i] = byte(i%255) + 1
	}
	for way := range c.cacheTag {
		for e := range c.cacheTag[way] {
			c.cacheTag[way][e] = uint32(way*1000 + e + 1)
			c.cacheValid[way][e] = true
		}
	}
	for i := range c.cacheLRU {
		c.cacheLRU[i] = byte(i&0x3F) + 1
	}

	c.intc.ipra = 0xF001
	c.intc.iprb = 0xF002
	c.intc.vcra = 0xF003
	c.intc.vcrb = 0xF004
	c.intc.vcrc = 0xF005
	c.intc.vcrd = 0xF006
	c.intc.icr = 0x8001
	c.intc.vcrwdt = 0xF007
	c.intc.pending = 0x0042

	c.frt.frc = 0xA001
	c.frt.ocra = 0xA002
	c.frt.ocrb = 0xA003
	c.frt.icr = 0xA004
	c.frt.tier = 0x8D
	c.frt.ftcsr = 0x8F
	c.frt.readFlags = 0x88
	c.frt.tcr = 0x03
	c.frt.tocr = 0x1F
	c.frt.prescaler = 0x77
	c.frt.temp = 0xAB
	c.frt.lastSync = 555666777
	c.frt.nextEvent = 888999000

	c.divu.dvsr = 0xB0000001
	c.divu.dvdnt = 0xB0000002
	c.divu.dvdnth = 0xB0000003
	c.divu.dvdntl = 0xB0000004
	c.divu.dvcr = 0x00000003
	c.divu.vcrdiv = 0x0000006F

	for i := range c.dmac.ch {
		c.dmac.ch[i].sar = 0xC0000001 + uint32(i)
		c.dmac.ch[i].dar = 0xC0000011 + uint32(i)
		c.dmac.ch[i].tcr = 0x00C001 + uint32(i)
		c.dmac.ch[i].chcr = 0x0000C1 + uint32(i)
		c.dmac.ch[i].vcrdma = 0x60 + uint32(i)
	}
	c.dmac.dmaor = 0x0001
	c.dmac.drcr = [2]uint8{0x01, 0x02}
	c.dmac.nextCh = 1
	c.dmac.stallCycles = 23
	c.dmac.stallCh = 1

	c.wdt.wtcsr = 0x25
	c.wdt.wtcnt = 0x9C
	c.wdt.rstcsr = 0x60
	c.wdt.prescaler = 0x1234
	c.wdt.lastSync = 121314151
	c.wdt.nextEvent = 617181920
}

// checkNoZeroLeaf recursively walks v and fails on any numeric leaf
// that is zero or bool leaf that is false. Used against a State taken
// from a fully-filled CPU: a State field that was never assigned in
// State() stays zero and is caught here.
func checkNoZeroLeaf(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			checkNoZeroLeaf(t, v.Field(i), path+"."+v.Type().Field(i).Name)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			checkNoZeroLeaf(t, v.Index(i), path)
		}
	case reflect.Bool:
		if !v.Bool() {
			t.Errorf("%s: false after fill; State() does not copy it", path)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.Int() == 0 {
			t.Errorf("%s: zero after fill; State() does not copy it", path)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v.Uint() == 0 {
			t.Errorf("%s: zero after fill; State() does not copy it", path)
		}
	default:
		t.Errorf("%s: unhandled kind %s", path, v.Kind())
	}
}

// TestStateCapturesEverything fills every captured CPU field with a
// non-zero value, takes a snapshot, and requires every State leaf to be
// non-zero: a State field with no copy-out wiring fails. Then restores
// into a fresh CPU and requires the second snapshot to be identical:
// asymmetric SetState wiring fails.
func TestStateCapturesEverything(t *testing.T) {
	c := New(newTestBus(4096), true)
	fillCPUState(c)

	s := c.State()
	checkNoZeroLeaf(t, reflect.ValueOf(s), "State")

	c2 := New(newTestBus(4096), true)
	c2.SetState(&s)
	if s2 := c2.State(); s2 != s {
		t.Errorf("State -> SetState -> State round-trip mismatch")
	}
}

// TestStateSpotValues verifies a sample of snapshot fields carry the
// exact values set, guarding against crossed copy wiring (field A
// copied into field B) that the non-zero walk cannot see.
func TestStateSpotValues(t *testing.T) {
	c := New(newTestBus(4096), true)
	fillCPUState(c)
	s := c.State()

	if s.Reg.PC != 0x06000100 {
		t.Errorf("Reg.PC = %08x", s.Reg.PC)
	}
	if s.IRL != 0x000E0047 {
		t.Errorf("IRL = %08x", s.IRL)
	}
	if s.Exec.PendingVal2 != 0x22222222 {
		t.Errorf("Exec.PendingVal2 = %08x", s.Exec.PendingVal2)
	}
	if s.Exec.FetchLineWay != 2 {
		t.Errorf("Exec.FetchLineWay = %d", s.Exec.FetchLineWay)
	}
	if s.Cache.Tag[2][10] != 2011 {
		t.Errorf("Cache.Tag[2][10] = %d", s.Cache.Tag[2][10])
	}
	if s.INTC.Pending != 0x0042 {
		t.Errorf("INTC.Pending = %04x", s.INTC.Pending)
	}
	if s.FRT.Temp != 0xAB {
		t.Errorf("FRT.Temp = %02x", s.FRT.Temp)
	}
	if s.FRT.ReadFlags != 0x88 {
		t.Errorf("FRT.ReadFlags = %02x", s.FRT.ReadFlags)
	}
	if s.DIVU.DVDNTH != 0xB0000003 {
		t.Errorf("DIVU.DVDNTH = %08x", s.DIVU.DVDNTH)
	}
	if s.DMAC.Ch[1].VCRDMA != 0x61 {
		t.Errorf("DMAC.Ch[1].VCRDMA = %08x", s.DMAC.Ch[1].VCRDMA)
	}
	if s.WDT.NextEvent != 617181920 {
		t.Errorf("WDT.NextEvent = %d", s.WDT.NextEvent)
	}
}

// stateCoverage maps each struct type in the CPU's state graph to the
// classification of every field: captured by State(), or skipped with
// the reason class (per-cycle scratch set fresh before any read, or
// construction wiring). A new field added to any of these structs
// fails TestStateFieldCoverage until it is classified here AND, when
// captured, wired into State()/SetState() (TestStateCapturesEverything
// enforces the wiring).
var stateCoverage = map[reflect.Type]map[string]string{
	reflect.TypeOf(CPU{}): {
		"reg":                 "captured",
		"bus":                 "skip-construction",
		"cycles":              "captured",
		"frameCyc":            "skip-scratch",
		"ir":                  "skip-scratch",
		"halted":              "captured",
		"addrError":           "skip-scratch",
		"prevPC":              "captured",
		"delayPC":             "captured",
		"inDelay":             "captured",
		"nmiPending":          "captured",
		"nmiReq":              "captured",
		"intInhibit":          "captured",
		"irl":                 "captured",
		"irlAck":              "skip-construction",
		"pendingOp":           "captured",
		"pendingStep":         "captured",
		"pendingCount":        "captured",
		"pendingN":            "captured",
		"pendingAddr":         "captured",
		"pendingVal":          "captured",
		"pendingVal2":         "captured",
		"pendingImm":          "captured",
		"lastLoadReg":         "captured",
		"deferredOp":          "captured",
		"hasDeferred":         "captured",
		"loadUseStall":        "captured",
		"multiplierBusyUntil": "captured",
		"stepBus":             "skip-scratch",
		"branchTaken":         "skip-scratch",
		"intc":                "captured",
		"frt":                 "captured",
		"divu":                "captured",
		"dmac":                "captured",
		"wdt":                 "captured",
		"nextPeripheralEvent": "captured",
		"cacheData":           "captured",
		"cacheTag":            "captured",
		"cacheValid":          "captured",
		"cacheLRU":            "captured",
		"ccr":                 "captured",
		"fetchLineAddr":       "captured",
		"fetchLineWay":        "captured",
		"fetchLineOff":        "captured",
		"busStall":            "captured",
		"sbycr":               "captured",
		"bcr1":                "captured",
		"isMaster":            "skip-construction",
		"TraceFunc":           "skip-construction",
		"HLEHook":             "skip-construction",
	},
	reflect.TypeOf(INTC{}): {
		"ipra": "captured", "iprb": "captured",
		"vcra": "captured", "vcrb": "captured",
		"vcrc": "captured", "vcrd": "captured",
		"icr": "captured", "vcrwdt": "captured",
		"pending": "captured",
	},
	reflect.TypeOf(FRT{}): {
		"frc": "captured", "ocra": "captured", "ocrb": "captured",
		"icr": "captured", "tier": "captured", "ftcsr": "captured",
		"readFlags": "captured", "tcr": "captured", "tocr": "captured",
		"prescaler": "captured", "temp": "captured",
		"lastSync": "captured", "nextEvent": "captured",
	},
	reflect.TypeOf(DIVU{}): {
		"dvsr": "captured", "dvdnt": "captured", "dvdnth": "captured",
		"dvdntl": "captured", "dvcr": "captured", "vcrdiv": "captured",
	},
	reflect.TypeOf(DMAC{}): {
		"ch": "captured", "dmaor": "captured", "drcr": "captured",
		"nextCh": "captured", "bus": "skip-construction",
		"stallCycles": "captured", "stallCh": "captured",
	},
	reflect.TypeOf(dmaChan{}): {
		"sar": "captured", "dar": "captured", "tcr": "captured",
		"chcr": "captured", "vcrdma": "captured",
	},
	reflect.TypeOf(WDT{}): {
		"wtcsr": "captured", "wtcnt": "captured", "rstcsr": "captured",
		"prescaler": "captured", "lastSync": "captured",
		"nextEvent": "captured",
	},
}

// TestStateFieldCoverage requires every field of every struct in the
// CPU state graph to be classified in stateCoverage, and every
// classified name to still exist. Catches new fields silently missing
// from save states and stale classifications.
func TestStateFieldCoverage(t *testing.T) {
	for typ, fields := range stateCoverage {
		seen := make(map[string]bool)
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			seen[name] = true
			if _, ok := fields[name]; !ok {
				t.Errorf("%s.%s: not classified in stateCoverage; add it to State() or the skip list", typ.Name(), name)
			}
		}
		for name := range fields {
			if !seen[name] {
				t.Errorf("%s.%s: classified in stateCoverage but no longer exists", typ.Name(), name)
			}
		}
	}
}
