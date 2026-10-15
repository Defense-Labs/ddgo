package frontend

import "github.com/ianbruene/ddgo/internal/app"

// ShutdownAttemptID distinguishes user-initiated application shutdown attempts.
// A late result from an abandoned attempt must not affect a later attempt.
type ShutdownAttemptID uint64

type ShutdownPhase uint8

const (
	ShutdownIdle ShutdownPhase = iota
	ShutdownConfirming
	ShutdownWaitingForStopConfirmation
	ShutdownWaitingForStop
	ShutdownClosingDocuments
	ShutdownReadyToCloseMain
	ShutdownFinished
)

// ShutdownStopTarget identifies the one execution an approved shutdown may
// stop. A zero DocumentID is a valid legacy run; RunID is always nonzero.
type ShutdownStopTarget struct {
	DocumentID app.DocumentID
	RunID      app.RunID
}

func (t ShutdownStopTarget) matches(state app.State) bool {
	return t.RunID != 0 && state.ProgramDocumentID == t.DocumentID && state.ProgramRunID == t.RunID
}

// ShutdownCoordinator owns the toolkit-independent state of one requested
// application shutdown. Callers consume its transitions on the UI thread;
// it never manipulates native windows or starts controller work itself.
type ShutdownCoordinator struct {
	nextAttempt ShutdownAttemptID
	attempt     ShutdownAttemptID
	phase       ShutdownPhase
	stopTarget  ShutdownStopTarget
	stopOwned   bool
	documents   map[app.DocumentID]bool // false until native closure is observed
	// selectedDocument is the one native document closure currently authorized
	// by this attempt. A close callback for any other document must still take
	// the ordinary independent-close path.
	selectedDocument app.DocumentID
	finalIssued      bool
}

func (c *ShutdownCoordinator) Phase() ShutdownPhase       { return c.phase }
func (c *ShutdownCoordinator) Attempt() ShutdownAttemptID { return c.attempt }
func (c *ShutdownCoordinator) Active() bool {
	return c.phase != ShutdownIdle && c.phase != ShutdownFinished
}

// Begin records a preflight snapshot. The caller must still obtain the user
// confirmation through ConfirmApplication before any document is closed.
func (c *ShutdownCoordinator) Begin(documents []app.DocumentID, target ShutdownStopTarget) (ShutdownAttemptID, bool) {
	if c.Active() || c.finalIssued {
		return 0, false
	}
	c.nextAttempt++
	if c.nextAttempt == 0 {
		c.nextAttempt++
	}
	c.attempt = c.nextAttempt
	c.phase = ShutdownConfirming
	c.stopTarget = target
	c.stopOwned = false
	c.documents = make(map[app.DocumentID]bool, len(documents))
	for _, id := range documents {
		if id != 0 {
			c.documents[id] = false
		}
	}
	return c.attempt, true
}

// ConfirmApplication advances an attempt after the application-level dialog.
// False fully cancels the attempt before any native document close or Stop.
func (c *ShutdownCoordinator) ConfirmApplication(attempt ShutdownAttemptID, accepted bool) ShutdownPhase {
	if !c.current(attempt) || c.phase != ShutdownConfirming {
		return c.phase
	}
	if !accepted {
		c.cancelCurrent()
		return c.phase
	}
	if c.stopTarget.RunID != 0 {
		c.phase = ShutdownWaitingForStopConfirmation
	} else {
		c.phase = ShutdownClosingDocuments
	}
	return c.phase
}

// ConfirmStop either abandons shutdown before controller I/O or grants the
// caller ownership to launch exactly one run-aware Stop request.
func (c *ShutdownCoordinator) ConfirmStop(attempt ShutdownAttemptID, accepted bool) (ShutdownStopTarget, bool) {
	if !c.current(attempt) || c.phase != ShutdownWaitingForStopConfirmation {
		return ShutdownStopTarget{}, false
	}
	if !accepted {
		c.cancelCurrent()
		return ShutdownStopTarget{}, false
	}
	c.phase = ShutdownWaitingForStop
	c.stopOwned = true
	return c.stopTarget, true
}

// StopResult accepts only the result belonging to the current, coordinator-
// owned Stop. Verified success begins document closure; failure cancels intent.
func (c *ShutdownCoordinator) StopResult(attempt ShutdownAttemptID, target ShutdownStopTarget, verified bool) bool {
	if !c.current(attempt) || c.phase != ShutdownWaitingForStop || !c.stopOwned || target != c.stopTarget {
		return false
	}
	if !verified {
		c.cancelCurrent()
		return false
	}
	c.stopOwned = false
	c.phase = ShutdownClosingDocuments
	return true
}

// StopAlreadyInProgress relinquishes this attempt's request ownership while
// retaining the exact target. The application then observes the controller's
// authoritative stop outcome instead of issuing a competing abort sequence.
func (c *ShutdownCoordinator) StopAlreadyInProgress(attempt ShutdownAttemptID, target ShutdownStopTarget) bool {
	if !c.current(attempt) || c.phase != ShutdownWaitingForStop || target != c.stopTarget {
		return false
	}
	c.stopOwned = false
	return true
}

// ObserveExternalStop handles the controller's pre-existing Stop coordinator.
// It never claims ownership, but can finish an already-current exact run.
func (c *ShutdownCoordinator) ObserveExternalStop(state app.State) bool {
	if c.phase != ShutdownWaitingForStop {
		return false
	}
	if !c.stopTarget.matches(state) {
		// A later execution must never inherit this attempt's Stop or document
		// close authority. A zero RunID means the original terminal result is no
		// longer represented by current state, so successful Stop cannot be
		// established either.
		if state.ProgramRunID == 0 || state.ProgramRunID != c.stopTarget.RunID || state.ProgramDocumentID != c.stopTarget.DocumentID {
			c.cancelCurrent()
		}
		return false
	}
	if state.ProgramStatus == app.ProgramStopped && state.MachineReadiness == app.MachineReadinessNormal {
		c.stopOwned = false
		c.phase = ShutdownClosingDocuments
		return true
	}
	if state.ProgramStatus == app.ProgramFailed || state.ProgramStatus == app.ProgramCompleted ||
		state.MachineReadiness == app.MachineReadinessRecoveryRequired {
		c.cancelCurrent()
	}
	return false
}

// NextDocumentToClose returns one not-yet-observed native document closure.
// The caller performs the close outside this method and reports the outcome.
func (c *ShutdownCoordinator) NextDocumentToClose(attempt ShutdownAttemptID) (app.DocumentID, bool) {
	if !c.current(attempt) || c.phase != ShutdownClosingDocuments {
		return 0, false
	}
	if c.selectedDocument != 0 {
		// Its native Close was already requested. Do not issue another request
		// or advance to a second document until acceptance/rejection is known.
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
// attempt, so independent or stale close callbacks are harmless.
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

// DocumentCloseRejected genuinely cancels the shutdown attempt. A later
// independent closure must not resume a user-cancelled shutdown.
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

// AllowsDocumentClose lets the application distinguish its own planned native
// close from an unrelated user close while shutdown is in progress.
func (c *ShutdownCoordinator) AllowsDocumentClose(attempt ShutdownAttemptID, id app.DocumentID) bool {
	if !c.current(attempt) || c.phase != ShutdownClosingDocuments {
		return false
	}
	closed, ok := c.documents[id]
	return ok && !closed && c.selectedDocument == id
}

// CanCloseSelectedDocument combines exact shutdown authorization with the
// controller-state revalidation required immediately before native closure.
// It intentionally does not mutate the attempt: the UI decides whether to
// report a rejection and cancel through DocumentCloseRejected.
func (c *ShutdownCoordinator) CanCloseSelectedDocument(attempt ShutdownAttemptID, id app.DocumentID, state app.State) bool {
	return c.AllowsDocumentClose(attempt, id) && !state.ProgramStatus.IsActive() &&
		state.MachineReadiness == app.MachineReadinessNormal
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

// Cancel invalidates an attempt. If a physical Stop has already been accepted,
// its controller lifecycle continues; only automatic application closure is
// cancelled and late results become no-ops.
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
	c.stopTarget = ShutdownStopTarget{}
	c.stopOwned = false
	c.documents = nil
	c.selectedDocument = 0
}
