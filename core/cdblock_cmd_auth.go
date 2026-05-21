// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

func (cb *CDBlock) cmdAuthenticateDisc() {
	mpegAuth := cb.cmd[1] & 0xFF

	if mpegAuth == 1 {
		// MPEG card authentication request - no MPEG card present
		cb.standardReturn()
		cb.hirqReq |= hirqCMOK | hirqMPED
	} else {
		// Disc authentication request
		if cb.disc != nil {
			cb.authenticated = true
			cb.status = cdStatusBusy
		}
		cb.standardReturn()
		if cb.disc != nil {
			cb.status = cdStatusPause
		}
		cb.resultsRead = true
		cb.hirqReq = hirqCMOK | hirqCSCT | hirqESEL | hirqEHST | hirqECPY | hirqEFLS | hirqSCDQ
	}
}

func (cb *CDBlock) cmdGetAuthStatus() {
	var cr1 uint16
	if cb.cmd[1] == 0 && cb.authenticated && cb.disc != nil {
		cr1 = uint16(cb.discType)
	}
	cb.setResponse(cr1, 0, 0)
	cb.hirqReq |= hirqCMOK
}
