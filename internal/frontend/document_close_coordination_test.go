package frontend

import (
	"errors"
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/gcode"
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
		{"draining is still active", nil, true, app.State{ProgramDocumentID: documentID, ProgramRunID: runID, ProgramStatus: app.ProgramDraining, MachineReadiness: app.MachineReadinessNormal}, false, DocumentStopInvalid},
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

func TestResolveDeferredAdmissionClose(t *testing.T) {
	for _, tt := range []struct {
		name    string
		pending bool
		runID   app.RunID
		err     error
		exact   bool
		want    DeferredAdmissionCloseDisposition
	}{
		{"not deferred", false, 11, nil, true, DeferredAdmissionCloseNone},
		{"rejected admission retries normal close", true, 0, errors.New("controller busy"), false, DeferredAdmissionCloseRetryNormal},
		{"zero run never waits forever", true, 0, nil, false, DeferredAdmissionCloseRetryNormal},
		{"successful admission awaits its exact state", true, 11, nil, false, DeferredAdmissionCloseWaitForExactRun},
		{"exact active or terminal state reissues normal policy", true, 11, nil, true, DeferredAdmissionCloseReissueNormal},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveDeferredAdmissionClose(tt.pending, tt.runID, tt.err, tt.exact); got != tt.want {
				t.Fatalf("ResolveDeferredAdmissionClose() = %v, want %v", got, tt.want)
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

func TestDocumentCloseApprovalSupersessionReleasesRegistryForLaterClose(t *testing.T) {
	registry, _ := newTestDocumentRegistry(t)
	path := writeDocument(t, "approval.gcode", "G0 X1\n")
	var windows []*fakeDocumentWindow
	entry, opened, err := registry.OpenFile(path, gcode.LoadDocument, testDocumentWindowFactory(&windows))
	if err != nil || !opened {
		t.Fatalf("OpenFile() = (%v, %v)", opened, err)
	}
	if !registry.MarkClosePending(entry.ID) {
		t.Fatal("could not mark initial close pending")
	}
	approvals := DocumentCloseApprovals{entry.ID: 10}
	if !approvals.InvalidateSuperseded(registry, entry.ID, app.State{ProgramDocumentID: entry.ID, ProgramRunID: 11, ProgramStatus: app.ProgramRunning}) {
		t.Fatal("replacement run did not invalidate approval")
	}
	if registry.ClosePending(entry.ID) {
		t.Fatal("superseded approval stranded ClosePending")
	}
	if _, ok := approvals[entry.ID]; ok {
		t.Fatal("superseded run retained close approval")
	}
	if !registry.MarkClosePending(entry.ID) {
		t.Fatal("later legitimate close could not begin")
	}
}
