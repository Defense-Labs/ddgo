package frontend

import (
	"errors"
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
)

func stoppedDocumentState(documentID app.DocumentID, runID app.RunID) app.State {
	return app.State{
		ProgramDocumentID: documentID,
		ProgramRunID:      runID,
		ProgramStatus:     app.ProgramStopped,
		MachineReadiness:  app.MachineReadinessNormal,
	}
}

func TestClassifyDocumentStopResult(t *testing.T) {
	const documentID app.DocumentID = 7
	const runID app.RunID = 11
	for _, tt := range []struct {
		name     string
		err      error
		open     bool
		state    app.State
		shutdown bool
		want     DocumentStopDisposition
	}{
		{"independent success", nil, true, stoppedDocumentState(documentID, runID), false, DocumentStopCloseIndependently},
		{"shutdown transfer", nil, true, stoppedDocumentState(documentID, runID), true, DocumentStopTransferToShutdown},
		{"controller failure", errors.New("stop failed"), true, stoppedDocumentState(documentID, runID), true, DocumentStopInvalid},
		{"recovery required", nil, true, app.State{ProgramDocumentID: documentID, ProgramRunID: runID, ProgramStatus: app.ProgramFailed, MachineReadiness: app.MachineReadinessRecoveryRequired}, true, DocumentStopInvalid},
		{"replacement run", nil, true, stoppedDocumentState(documentID, runID+1), true, DocumentStopInvalid},
		{"closed old document", nil, false, stoppedDocumentState(documentID, runID), false, DocumentStopInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyDocumentStopResult(tt.err, tt.open, documentID, runID, tt.state, tt.shutdown); got != tt.want {
				t.Fatalf("ClassifyDocumentStopResult() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDocumentCloseApprovalSuperseded(t *testing.T) {
	const documentID app.DocumentID = 7
	const runID app.RunID = 11
	if DocumentCloseApprovalSuperseded(documentID, runID, app.State{ProgramDocumentID: documentID, ProgramRunID: runID, ProgramStatus: app.ProgramRunning}) {
		t.Fatal("approval was superseded by its own run")
	}
	if !DocumentCloseApprovalSuperseded(documentID, runID, app.State{ProgramDocumentID: documentID, ProgramRunID: runID + 1, ProgramStatus: app.ProgramRunning}) {
		t.Fatal("approval remained valid for replacement run")
	}
	if DocumentCloseApprovalSuperseded(documentID, runID, app.State{ProgramDocumentID: documentID + 1, ProgramRunID: runID + 1, ProgramStatus: app.ProgramRunning}) {
		t.Fatal("other document superseded approval")
	}
}
