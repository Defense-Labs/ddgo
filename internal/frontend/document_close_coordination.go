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
