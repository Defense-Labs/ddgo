package frontend

import (
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
)

func beginShutdown(t *testing.T, documents []app.DocumentID, target ShutdownStopTarget) (*ShutdownCoordinator, ShutdownAttemptID) {
	t.Helper()
	var coordinator ShutdownCoordinator
	attempt, ok := coordinator.Begin(documents, target)
	if !ok || attempt == 0 {
		t.Fatalf("Begin() = %d, %v", attempt, ok)
	}
	return &coordinator, attempt
}

func TestShutdownCoordinatorCancelledConfirmationCannotResume(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{1, 2}, ShutdownStopTarget{})
	if phase := coordinator.ConfirmApplication(attempt, false); phase != ShutdownIdle {
		t.Fatalf("cancelled confirmation phase = %v, want idle", phase)
	}
	if _, ok := coordinator.NextDocumentToClose(attempt); ok || coordinator.DocumentClosed(attempt, 1) || coordinator.CommitMainClose(attempt) {
		t.Fatal("cancelled attempt still accepted shutdown work")
	}
	if next, ok := coordinator.Begin([]app.DocumentID{1}, ShutdownStopTarget{}); !ok || next == attempt {
		t.Fatalf("new attempt = %d, %v after cancellation", next, ok)
	}
}

func TestShutdownCoordinatorEmptyApplicationCommitsOneMainClose(t *testing.T) {
	coordinator, attempt := beginShutdown(t, nil, ShutdownStopTarget{})
	if phase := coordinator.ConfirmApplication(attempt, true); phase != ShutdownClosingDocuments {
		t.Fatalf("empty confirmation phase = %v", phase)
	}
	if _, ok := coordinator.NextDocumentToClose(attempt); ok || coordinator.Phase() != ShutdownReadyToCloseMain {
		t.Fatalf("empty shutdown did not become ready: %v", coordinator.Phase())
	}
	if !coordinator.CommitMainClose(attempt) || coordinator.CommitMainClose(attempt) {
		t.Fatal("empty shutdown main close was not exactly once")
	}
}

func TestShutdownCoordinatorStopAndFinalCloseAreExactOnce(t *testing.T) {
	target := ShutdownStopTarget{DocumentID: 7, RunID: 9}
	coordinator, attempt := beginShutdown(t, []app.DocumentID{7, 8}, target)
	if phase := coordinator.ConfirmApplication(attempt, true); phase != ShutdownWaitingForStopConfirmation {
		t.Fatalf("application confirmation phase = %v", phase)
	}
	gotTarget, ok := coordinator.ConfirmStop(attempt, true)
	if !ok || gotTarget != target {
		t.Fatalf("ConfirmStop() = %+v, %v", gotTarget, ok)
	}
	if _, ok := coordinator.ConfirmStop(attempt, true); ok {
		t.Fatal("duplicate stop confirmation acquired ownership")
	}
	if !coordinator.StopResult(attempt, target, true) || coordinator.Phase() != ShutdownClosingDocuments {
		t.Fatalf("successful stop did not begin document closure: %v", coordinator.Phase())
	}
	for range 2 {
		id, ok := coordinator.NextDocumentToClose(attempt)
		if !ok || !coordinator.AllowsDocumentClose(attempt, id) || !coordinator.DocumentClosed(attempt, id) {
			t.Fatalf("document close transition failed for %d", id)
		}
	}
	if _, ok := coordinator.NextDocumentToClose(attempt); ok || coordinator.Phase() != ShutdownReadyToCloseMain {
		t.Fatalf("documents did not reach ready-to-close-main: %v", coordinator.Phase())
	}
	if !coordinator.CommitMainClose(attempt) || coordinator.CommitMainClose(attempt) {
		t.Fatal("main close was not committed exactly once")
	}
}

func TestShutdownCoordinatorFailedAndStaleStopResultsDoNotClose(t *testing.T) {
	target := ShutdownStopTarget{DocumentID: 4, RunID: 5}
	coordinator, first := beginShutdown(t, []app.DocumentID{4}, target)
	coordinator.ConfirmApplication(first, true)
	coordinator.ConfirmStop(first, true)
	if coordinator.StopResult(first, target, false) || coordinator.Phase() != ShutdownIdle {
		t.Fatalf("failed stop phase = %v", coordinator.Phase())
	}
	second, ok := coordinator.Begin([]app.DocumentID{6}, ShutdownStopTarget{})
	if !ok {
		t.Fatal("second shutdown attempt was rejected")
	}
	if coordinator.StopResult(first, target, true) || coordinator.Phase() != ShutdownConfirming {
		t.Fatalf("stale result affected second attempt: %v", coordinator.Phase())
	}
	coordinator.ConfirmApplication(second, true)
	if id, ok := coordinator.NextDocumentToClose(second); !ok || id != 6 {
		t.Fatalf("second attempt next document = %d, %v", id, ok)
	}
}

func TestShutdownCoordinatorObservesExistingExactStopOnly(t *testing.T) {
	target := ShutdownStopTarget{DocumentID: 0, RunID: 11}
	coordinator, attempt := beginShutdown(t, []app.DocumentID{3}, target)
	coordinator.ConfirmApplication(attempt, true)
	coordinator.ConfirmStop(attempt, true)
	if coordinator.ObserveExternalStop(app.State{ProgramDocumentID: 1, ProgramRunID: 11, ProgramStatus: app.ProgramStopped, MachineReadiness: app.MachineReadinessNormal}) {
		t.Fatal("replacement document run completed legacy shutdown")
	}
	if !coordinator.ObserveExternalStop(app.State{ProgramDocumentID: 0, ProgramRunID: 11, ProgramStatus: app.ProgramStopped, MachineReadiness: app.MachineReadinessNormal}) {
		t.Fatal("matching external stop was not observed")
	}
}

func TestShutdownCoordinatorRejectedDocumentCloseCancelsAttempt(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{1, 2}, ShutdownStopTarget{})
	coordinator.ConfirmApplication(attempt, true)
	if id, ok := coordinator.NextDocumentToClose(attempt); !ok || id == 0 {
		t.Fatal("missing document to close")
	} else if !coordinator.DocumentCloseRejected(attempt, id) || coordinator.Phase() != ShutdownIdle {
		t.Fatalf("rejected close phase = %v", coordinator.Phase())
	}
}
