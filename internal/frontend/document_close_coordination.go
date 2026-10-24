package frontend

import "github.com/ianbruene/ddgo/internal/app"

// DocumentStopDisposition describes what the UI may do after one exact,
// independently requested StopProgramRun returns. It keeps the native-window
// decision out of controller goroutines and is intentionally independent of
// Qt.
type DocumentStopDisposition uint8

// DocumentCloseApprovals retains independently verified Stop-and-Close
// approvals by their exact run. It is intentionally distinct from shutdown's
// selected-document authorization.
type DocumentCloseApprovals map[app.DocumentID]app.RunID

// DeferredAdmissionCloseDisposition is the narrow decision made when a Start
// result arrives for a document whose native close was deferred. It does not
// authorize Stop or destruction itself; the application still re-enters its
// ordinary close policy, which validates exact controller ownership.
type DeferredAdmissionCloseDisposition uint8

const (
	DeferredAdmissionCloseNone DeferredAdmissionCloseDisposition = iota
	DeferredAdmissionCloseRetryNormal
	DeferredAdmissionCloseWaitForExactRun
	DeferredAdmissionCloseReissueNormal
)

// ResolveDeferredAdmissionClose keeps a failed admission from stranding a
// close request, while requiring a successful admission to be observed under
// its exact RunID before normal close policy is reissued. A zero RunID is not
// an admitted execution and is treated like a rejection for close purposes.
func ResolveDeferredAdmissionClose(closePending bool, runID app.RunID, err error, exactRunObserved bool) DeferredAdmissionCloseDisposition {
	if !closePending {
		return DeferredAdmissionCloseNone
	}
	if err != nil || runID == 0 {
		return DeferredAdmissionCloseRetryNormal
	}
	if !exactRunObserved {
		return DeferredAdmissionCloseWaitForExactRun
	}
	return DeferredAdmissionCloseReissueNormal
}

const (
	DocumentStopInvalid DocumentStopDisposition = iota
	DocumentStopCloseIndependently
	DocumentStopTransferToShutdown
)

// ClassifyDocumentStopResult verifies that a Stop result still belongs to the
// original document/run and reached the controller's verified stopped state.
// During application shutdown, valid Stop completion transfers only native
// close ownership to the shutdown coordinator; it never starts a second Stop.
func ClassifyDocumentStopResult(err error, documentOpen bool, documentID app.DocumentID, runID app.RunID, state app.State, shutdownActive bool) DocumentStopDisposition {
	if err != nil || !documentOpen || runID == 0 || state.ProgramDocumentID != documentID || state.ProgramRunID != runID ||
		state.ProgramStatus != app.ProgramStopped || state.MachineReadiness != app.MachineReadinessNormal {
		return DocumentStopInvalid
	}
	if shutdownActive {
		return DocumentStopTransferToShutdown
	}
	return DocumentStopCloseIndependently
}

// DocumentCloseApprovalSuperseded reports whether an independently verified
// Stop-and-Close approval belongs to an older run for this document. It is a
// presentation/lifecycle check only; a stale approval must never authorize a
// native close or a new Stop request.
func DocumentCloseApprovalSuperseded(documentID app.DocumentID, approvedRunID app.RunID, state app.State) bool {
	return approvedRunID != 0 && state.ProgramStatus.IsActive() && state.ProgramDocumentID == documentID && state.ProgramRunID != approvedRunID
}

// InvalidateSuperseded releases both an obsolete approval and the registry's
// close-pending flag. A later explicit close can then safely start its own
// exact-run lifecycle instead of being stranded by the old request.
func (a DocumentCloseApprovals) InvalidateSuperseded(registry *DocumentRegistry, documentID app.DocumentID, state app.State) bool {
	runID, ok := a[documentID]
	if !ok || !DocumentCloseApprovalSuperseded(documentID, runID, state) {
		return false
	}
	delete(a, documentID)
	if registry != nil {
		registry.ClearClosePending(documentID)
	}
	return true
}
