package frontend

import "github.com/ianbruene/ddgo/internal/app"

// ShutdownAttemptID distinguishes user-initiated application shutdown attempts.
// A callback from an abandoned attempt must not affect a later attempt.
type ShutdownAttemptID uint64

type ShutdownPhase uint8

const (
	ShutdownIdle ShutdownPhase = iota
	ShutdownConfirming
	ShutdownClosingDocuments
	ShutdownReadyToCloseMain
	ShutdownFinished
)

// ShutdownCoordinator owns toolkit-independent application and document
// lifetime during one requested exit. It deliberately has no controller or
// machine-state dependency: exiting DDGo never issues or verifies a machine
// command.
type ShutdownCoordinator struct {
	nextAttempt ShutdownAttemptID
	attempt     ShutdownAttemptID
	phase       ShutdownPhase
	documents   map[app.DocumentID]bool // false until native closure is observed
	// selectedDocument is the one native document closure currently authorized
	// by this attempt. A callback for another document remains independent.
	selectedDocument app.DocumentID
	finalIssued      bool
}

func (c *ShutdownCoordinator) Phase() ShutdownPhase       { return c.phase }
func (c *ShutdownCoordinator) Attempt() ShutdownAttemptID { return c.attempt }
func (c *ShutdownCoordinator) Active() bool {
	return c.phase != ShutdownIdle && c.phase != ShutdownFinished
}

// Begin records the open logical documents before ordinary exit confirmation.
func (c *ShutdownCoordinator) Begin(documents []app.DocumentID) (ShutdownAttemptID, bool) {
	if c.Active() || c.finalIssued {
		return 0, false
	}
	c.nextAttempt++
	if c.nextAttempt == 0 {
		c.nextAttempt++
	}
	c.attempt = c.nextAttempt
	c.phase = ShutdownConfirming
	c.documents = make(map[app.DocumentID]bool, len(documents))
	for _, id := range documents {
		if id != 0 {
			c.documents[id] = false
		}
	}
	return c.attempt, true
}

// ConfirmApplication advances an accepted ordinary exit directly to document
// closure. It intentionally does not wait for program admission, controller
// I/O, Stop, readiness, or recovery.
func (c *ShutdownCoordinator) ConfirmApplication(attempt ShutdownAttemptID, accepted bool) ShutdownPhase {
	if !c.current(attempt) || c.phase != ShutdownConfirming {
		return c.phase
	}
	if !accepted {
		c.cancelCurrent()
		return c.phase
	}
	c.phase = ShutdownClosingDocuments
	return c.phase
}

// NextDocumentToClose returns one not-yet-observed native document closure.
// The caller performs the close outside this method and reports its outcome.
func (c *ShutdownCoordinator) NextDocumentToClose(attempt ShutdownAttemptID) (app.DocumentID, bool) {
	if !c.current(attempt) || c.phase != ShutdownClosingDocuments {
		return 0, false
	}
	if c.selectedDocument != 0 {
		return 0, false
	}
	for id, closed := range c.documents {
		if !closed {
			c.selectedDocument = id
			return id, true
		}
	}
	c.phase = ShutdownReadyToCloseMain
	return 0, false
}

// DocumentClosed records native acceptance. Unknown IDs do not affect an
// attempt, so independent or stale callbacks are harmless.
func (c *ShutdownCoordinator) DocumentClosed(attempt ShutdownAttemptID, id app.DocumentID) bool {
	if !c.current(attempt) || c.phase != ShutdownClosingDocuments {
		return false
	}
	if _, ok := c.documents[id]; !ok || c.selectedDocument != id {
		return false
	}
	c.documents[id] = true
	c.selectedDocument = 0
	return true
}

// DocumentCloseRejected cancels the exit attempt. This is a native-document
// lifecycle result, not a machine-state interlock.
func (c *ShutdownCoordinator) DocumentCloseRejected(attempt ShutdownAttemptID, id app.DocumentID) bool {
	if !c.current(attempt) || c.phase != ShutdownClosingDocuments {
		return false
	}
	if _, ok := c.documents[id]; !ok || c.selectedDocument != id {
		return false
	}
	c.cancelCurrent()
	return true
}

// AllowsDocumentClose distinguishes the coordinator's selected native close
// from an unrelated user-requested document close.
func (c *ShutdownCoordinator) AllowsDocumentClose(attempt ShutdownAttemptID, id app.DocumentID) bool {
	if !c.current(attempt) || c.phase != ShutdownClosingDocuments {
		return false
	}
	closed, ok := c.documents[id]
	return ok && !closed && c.selectedDocument == id
}

// CommitMainClose is the exact-once final-close commitment. It must be called
// immediately before the UI invokes the native main-window Close method.
func (c *ShutdownCoordinator) CommitMainClose(attempt ShutdownAttemptID) bool {
	if !c.current(attempt) || c.phase != ShutdownReadyToCloseMain || c.finalIssued {
		return false
	}
	c.finalIssued = true
	c.phase = ShutdownFinished
	return true
}

func (c *ShutdownCoordinator) Cancel(attempt ShutdownAttemptID) bool {
	if !c.current(attempt) || c.phase == ShutdownFinished {
		return false
	}
	c.cancelCurrent()
	return true
}

func (c *ShutdownCoordinator) current(attempt ShutdownAttemptID) bool {
	return attempt != 0 && attempt == c.attempt
}

func (c *ShutdownCoordinator) cancelCurrent() {
	c.phase = ShutdownIdle
	c.attempt = 0
	c.documents = nil
	c.selectedDocument = 0
}
