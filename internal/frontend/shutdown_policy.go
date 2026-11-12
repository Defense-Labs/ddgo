package frontend

import "github.com/ianbruene/ddgo/internal/app"

// AllowsApplicationExit records DDGo's application-exit policy. Machine
// state and outstanding frontend program-start admissions intentionally do
// not restrict a user-confirmed exit; they remain inputs so this policy is
// explicit and regression-tested independently of a concrete UI toolkit.
func AllowsApplicationExit(app.State, uint64) bool { return true }

// ShutdownAsyncResultKind identifies a frontend result whose owner may be
// affected by the ordinary exit dialog.
type ShutdownAsyncResultKind uint8

const (
	ShutdownAsyncStartResult ShutdownAsyncResultKind = iota
	ShutdownAsyncActionResult
	ShutdownAsyncStopAndCloseResult
)

type ShutdownAsyncResultDisposition uint8

const (
	ShutdownAsyncApply ShutdownAsyncResultDisposition = iota
	ShutdownAsyncDefer
	ShutdownAsyncDiscard
)

// ShutdownAsyncDisposition keeps a cancellable confirmation distinct from
// committed teardown. Start and independent Stop-and-Close results can cause
// document-close side effects, so they wait for the confirmation outcome.
// Execution-action results can update their still-live session immediately.
// Once teardown is confirmed, no result may resurrect UI state.
func ShutdownAsyncDisposition(phase ShutdownPhase, kind ShutdownAsyncResultKind) ShutdownAsyncResultDisposition {
	switch phase {
	case ShutdownConfirming:
		if kind == ShutdownAsyncActionResult {
			return ShutdownAsyncApply
		}
		return ShutdownAsyncDefer
	case ShutdownClosingDocuments, ShutdownReadyToCloseMain, ShutdownFinished:
		return ShutdownAsyncDiscard
	default:
		return ShutdownAsyncApply
	}
}
