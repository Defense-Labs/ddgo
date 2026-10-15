package frontend

import (
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
)

func beginShutdown(t *testing.T, documents []app.DocumentID, target ShutdownStopTarget) (*ShutdownCoordinator, ShutdownAttemptID) {
	t.Helper()
	var coordinator ShutdownCoordinator
	attempt, ok := coordinator.Begin(documents)
	if !ok || attempt == 0 {
		t.Fatalf("Begin() = %d, %v", attempt, ok)
	}
	return &coordinator, attempt
}

func resolveRunAdmission(coordinator *ShutdownCoordinator, attempt ShutdownAttemptID, target ShutdownStopTarget) ShutdownPhase {
	state := app.State{MachineReadiness: app.MachineReadinessNormal}
	if target.RunID != 0 {
		state.ProgramDocumentID = target.DocumentID
		state.ProgramRunID = target.RunID
		state.ProgramStatus = app.ProgramRunning
	}
	return coordinator.ResolveRunAdmission(attempt, state)
}

func confirmShutdown(coordinator *ShutdownCoordinator, attempt ShutdownAttemptID, target ShutdownStopTarget) ShutdownPhase {
	coordinator.ConfirmApplication(attempt, true)
	return resolveRunAdmission(coordinator, attempt, target)
}

func TestShutdownCoordinatorCancelledConfirmationCannotResume(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{1, 2}, ShutdownStopTarget{})
	if phase := coordinator.ConfirmApplication(attempt, false); phase != ShutdownIdle {
		t.Fatalf("cancelled confirmation phase = %v, want idle", phase)
	}
	if _, ok := coordinator.NextDocumentToClose(attempt); ok || coordinator.DocumentClosed(attempt, 1) || coordinator.CommitMainClose(attempt) {
		t.Fatal("cancelled attempt still accepted shutdown work")
	}
	if next, ok := coordinator.Begin([]app.DocumentID{1}); !ok || next == attempt {
		t.Fatalf("new attempt = %d, %v after cancellation", next, ok)
	}
}

func TestShutdownCoordinatorEmptyApplicationCommitsOneMainClose(t *testing.T) {
	coordinator, attempt := beginShutdown(t, nil, ShutdownStopTarget{})
	if phase := confirmShutdown(coordinator, attempt, ShutdownStopTarget{}); phase != ShutdownClosingDocuments {
		t.Fatalf("empty confirmation phase = %v", phase)
	}
	if _, ok := coordinator.NextDocumentToClose(attempt); ok || coordinator.Phase() != ShutdownReadyToCloseMain {
		t.Fatalf("empty shutdown did not become ready: %v", coordinator.Phase())
	}
	if !coordinator.CommitMainClose(attempt) || coordinator.CommitMainClose(attempt) {
		t.Fatal("empty shutdown main close was not exactly once")
	}
}

func TestShutdownCoordinatorWaitsForFreshRunAdmissionState(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{8}, ShutdownStopTarget{})
	if phase := coordinator.ConfirmApplication(attempt, true); phase != ShutdownWaitingForRunAdmission {
		t.Fatalf("confirmation phase = %v, want waiting for run admission", phase)
	}
	if _, ok := coordinator.NextDocumentToClose(attempt); ok {
		t.Fatal("document closure began before run admission settled")
	}
	state := app.State{
		ProgramDocumentID: 8,
		ProgramRunID:      17,
		ProgramStatus:     app.ProgramRunning,
		MachineReadiness:  app.MachineReadinessNormal,
	}
	if phase := coordinator.ResolveRunAdmission(attempt, state); phase != ShutdownWaitingForStopConfirmation {
		t.Fatalf("fresh active run resolved to %v", phase)
	}
	target, ok := coordinator.ConfirmStop(attempt, true)
	if !ok || target.DocumentID != 8 || target.RunID != 17 {
		t.Fatalf("fresh stop target = %+v, %v", target, ok)
	}
}

func TestShutdownCoordinatorRunAdmissionFailureCanProceed(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{8}, ShutdownStopTarget{})
	coordinator.ConfirmApplication(attempt, true)
	if phase := coordinator.ResolveRunAdmission(attempt, app.State{MachineReadiness: app.MachineReadinessNormal}); phase != ShutdownClosingDocuments {
		t.Fatalf("failed/idle run admission resolved to %v", phase)
	}
}

func TestShutdownCoordinatorObservesAlreadyStoppingRun(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{8}, ShutdownStopTarget{})
	coordinator.ConfirmApplication(attempt, true)
	state := app.State{
		ProgramDocumentID: 8,
		ProgramRunID:      17,
		ProgramStatus:     app.ProgramStopping,
		MachineReadiness:  app.MachineReadinessStopping,
	}
	if phase := coordinator.ResolveRunAdmission(attempt, state); phase != ShutdownWaitingForStop {
		t.Fatalf("already-stopping run resolved to %v", phase)
	}
	if _, ok := coordinator.ConfirmStop(attempt, true); ok {
		t.Fatal("shutdown claimed a second Stop for an already-stopping run")
	}
}

func TestShutdownCoordinatorRestrictedReadinessCancelsRunAdmissionWait(t *testing.T) {
	coordinator, attempt := beginShutdown(t, nil, ShutdownStopTarget{})
	coordinator.ConfirmApplication(attempt, true)
	if phase := coordinator.ResolveRunAdmission(attempt, app.State{MachineReadiness: app.MachineReadinessRecoveryRequired}); phase != ShutdownIdle {
		t.Fatalf("restricted readiness resolved to %v", phase)
	}
}

func TestShutdownCoordinatorStopAndFinalCloseAreExactOnce(t *testing.T) {
	target := ShutdownStopTarget{DocumentID: 7, RunID: 9}
	coordinator, attempt := beginShutdown(t, []app.DocumentID{7, 8}, target)
	if phase := confirmShutdown(coordinator, attempt, target); phase != ShutdownWaitingForStopConfirmation {
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
	confirmShutdown(coordinator, first, target)
	coordinator.ConfirmStop(first, true)
	if coordinator.StopResult(first, target, false) || coordinator.Phase() != ShutdownIdle {
		t.Fatalf("failed stop phase = %v", coordinator.Phase())
	}
	second, ok := coordinator.Begin([]app.DocumentID{6})
	if !ok {
		t.Fatal("second shutdown attempt was rejected")
	}
	if coordinator.StopResult(first, target, true) || coordinator.Phase() != ShutdownConfirming {
		t.Fatalf("stale result affected second attempt: %v", coordinator.Phase())
	}
	confirmShutdown(coordinator, second, ShutdownStopTarget{})
	if id, ok := coordinator.NextDocumentToClose(second); !ok || id != 6 {
		t.Fatalf("second attempt next document = %d, %v", id, ok)
	}
}

func TestShutdownCoordinatorObservesExistingExactStopOnly(t *testing.T) {
	target := ShutdownStopTarget{DocumentID: 0, RunID: 11}
	coordinator, attempt := beginShutdown(t, []app.DocumentID{3}, target)
	confirmShutdown(coordinator, attempt, target)
	coordinator.ConfirmStop(attempt, true)
	coordinator.StopAlreadyInProgress(attempt, target)
	if coordinator.ObserveExternalStop(app.State{ProgramDocumentID: 1, ProgramRunID: 11, ProgramStatus: app.ProgramStopped, MachineReadiness: app.MachineReadinessNormal}) {
		t.Fatal("replacement document run completed legacy shutdown")
	}
	if coordinator.Phase() != ShutdownIdle {
		t.Fatalf("replacement run left shutdown waiting: %v", coordinator.Phase())
	}

	coordinator, attempt = beginShutdown(t, []app.DocumentID{3}, target)
	confirmShutdown(coordinator, attempt, target)
	coordinator.ConfirmStop(attempt, true)
	coordinator.StopAlreadyInProgress(attempt, target)
	if !coordinator.ObserveExternalStop(app.State{ProgramDocumentID: 0, ProgramRunID: 11, ProgramStatus: app.ProgramStopped, MachineReadiness: app.MachineReadinessNormal}) {
		t.Fatal("matching external stop was not observed")
	}
}

func TestShutdownCoordinatorRejectedDocumentCloseCancelsAttempt(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{1, 2}, ShutdownStopTarget{})
	confirmShutdown(coordinator, attempt, ShutdownStopTarget{})
	if id, ok := coordinator.NextDocumentToClose(attempt); !ok || id == 0 {
		t.Fatal("missing document to close")
	} else if !coordinator.DocumentCloseRejected(attempt, id) || coordinator.Phase() != ShutdownIdle {
		t.Fatalf("rejected close phase = %v", coordinator.Phase())
	}
}

func TestShutdownCoordinatorAuthorizesOnlySelectedDocument(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{1, 2, 3}, ShutdownStopTarget{})
	confirmShutdown(coordinator, attempt, ShutdownStopTarget{})
	first, ok := coordinator.NextDocumentToClose(attempt)
	if !ok {
		t.Fatal("missing first selected document")
	}
	if !coordinator.AllowsDocumentClose(attempt, first) {
		t.Fatalf("selected document %d was not authorized", first)
	}
	for _, id := range []app.DocumentID{1, 2, 3} {
		if id != first && coordinator.AllowsDocumentClose(attempt, id) {
			t.Fatalf("unselected document %d was authorized while %d was selected", id, first)
		}
	}
	if _, ok := coordinator.NextDocumentToClose(attempt); ok {
		t.Fatal("second document was selected while first closure remained outstanding")
	}
	for _, id := range []app.DocumentID{1, 2, 3} {
		if id != first && coordinator.DocumentClosed(attempt, id) {
			t.Fatalf("unselected document %d was accepted as a shutdown close", id)
		}
	}
	if coordinator.DocumentClosed(attempt, 99) || !coordinator.DocumentClosed(attempt, first) {
		t.Fatal("selected document closure was not recorded exactly")
	}
	second, ok := coordinator.NextDocumentToClose(attempt)
	if !ok || second == first || !coordinator.AllowsDocumentClose(attempt, second) {
		t.Fatalf("next selection = %d, %v after closing %d", second, ok, first)
	}
}

func TestShutdownCoordinatorStaleCloseAuthorizationCannotCrossAttempts(t *testing.T) {
	coordinator, first := beginShutdown(t, []app.DocumentID{7}, ShutdownStopTarget{})
	confirmShutdown(coordinator, first, ShutdownStopTarget{})
	id, ok := coordinator.NextDocumentToClose(first)
	if !ok || !coordinator.AllowsDocumentClose(first, id) {
		t.Fatal("first attempt did not select its document")
	}
	if !coordinator.Cancel(first) {
		t.Fatal("first attempt was not cancelled")
	}
	second, ok := coordinator.Begin([]app.DocumentID{7})
	if !ok {
		t.Fatal("second attempt was not created")
	}
	confirmShutdown(coordinator, second, ShutdownStopTarget{})
	if coordinator.AllowsDocumentClose(first, id) {
		t.Fatal("old attempt authorization survived cancellation")
	}
	if next, ok := coordinator.NextDocumentToClose(second); !ok || next != id || !coordinator.AllowsDocumentClose(second, id) {
		t.Fatalf("second attempt selection = %d, %v", next, ok)
	}
}

func TestShutdownCoordinatorRejectedSelectedCloseStopsFurtherProgress(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{1, 2}, ShutdownStopTarget{})
	confirmShutdown(coordinator, attempt, ShutdownStopTarget{})
	id, ok := coordinator.NextDocumentToClose(attempt)
	if !ok || !coordinator.DocumentCloseRejected(attempt, id) {
		t.Fatal("selected close rejection was not accepted")
	}
	if coordinator.Phase() != ShutdownIdle {
		t.Fatalf("rejected close phase = %v", coordinator.Phase())
	}
	if _, ok := coordinator.NextDocumentToClose(attempt); ok || coordinator.AllowsDocumentClose(attempt, 1) || coordinator.DocumentClosed(attempt, 2) {
		t.Fatal("cancelled shutdown accepted later document work")
	}
}

func TestShutdownCoordinatorRejectsSelectedCloseWhenExecutionChanges(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{6}, ShutdownStopTarget{})
	confirmShutdown(coordinator, attempt, ShutdownStopTarget{})
	id, ok := coordinator.NextDocumentToClose(attempt)
	if !ok {
		t.Fatal("missing selected document")
	}
	state := app.State{
		ProgramDocumentID: id,
		ProgramRunID:      44,
		ProgramStatus:     app.ProgramRunning,
		MachineReadiness:  app.MachineReadinessNormal,
	}
	if coordinator.CanCloseSelectedDocument(attempt, id, state) {
		t.Fatal("selected document was closeable after a new active execution")
	}
	if !coordinator.DocumentCloseRejected(attempt, id) || coordinator.Phase() != ShutdownIdle {
		t.Fatalf("execution change did not cancel shutdown: %v", coordinator.Phase())
	}
}

func TestShutdownCoordinatorExternalStopTerminalFailureCancels(t *testing.T) {
	target := ShutdownStopTarget{DocumentID: 4, RunID: 10}
	coordinator, attempt := beginShutdown(t, []app.DocumentID{4}, target)
	confirmShutdown(coordinator, attempt, target)
	coordinator.ConfirmStop(attempt, true)
	coordinator.StopAlreadyInProgress(attempt, target)
	if coordinator.ObserveExternalStop(app.State{ProgramDocumentID: 4, ProgramRunID: 10, ProgramStatus: app.ProgramFailed, MachineReadiness: app.MachineReadinessNormal}) {
		t.Fatal("failed stop was reported as successful")
	}
	if coordinator.Phase() != ShutdownIdle {
		t.Fatalf("failed external stop phase = %v", coordinator.Phase())
	}
}
