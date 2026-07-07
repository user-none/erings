// Copyright 2026 The erings Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package core

func (cb *CDBlock) cmdAuthenticateDisc() {
	// CR2 low byte selects the target: 0 = disc, 1 = MPEG card
	mpegAuth := cb.cmd[1] & 0xFF

	if mpegAuth == 1 {
		cb.mpegCardAuth.Store(true)
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
		cb.hirqReq |= hirqCMOK | hirqCSCT | hirqESEL | hirqEHST | hirqECPY | hirqEFLS | hirqSCDQ
	}
}

func (cb *CDBlock) cmdGetAuthStatus() {
	// CR2 low byte selects the target, same as $E0.
	var cr1 uint16
	switch cb.cmd[1] & 0xFF {
	case 0: // disc auth result
		if cb.authenticated && cb.disc != nil {
			cr1 = uint16(cb.discType)
		}
	case 1: // MPEG card auth result: SYS_CHKMPEG requires low byte 2
		if cb.mpegCardAuth.Load() {
			cr1 = 2
		}
	}
	cb.setResponse(cr1, 0, 0)
	cb.hirqReq |= hirqCMOK
}
