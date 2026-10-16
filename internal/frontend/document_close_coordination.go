package frontend

import "github.com/ianbruene/ddgo/internal/app"

// DocumentStopDisposition describes what the UI may do after one exact,
// independently requested StopProgramRun returns. It keeps the native-window
// decision out of controller goroutines and is intentionally independent of
// Qt.
type DocumentStopDisposition uint8

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
