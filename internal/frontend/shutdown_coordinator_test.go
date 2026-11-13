package frontend

import (
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
)

func beginShutdown(t *testing.T, documents []app.DocumentID) (*ShutdownCoordinator, ShutdownAttemptID) {
	t.Helper()
	var coordinator ShutdownCoordinator
	attempt, ok := coordinator.Begin(documents)
	if !ok || attempt == 0 {
		t.Fatalf("Begin() = %d, %v", attempt, ok)
	}
	return &coordinator, attempt
}

func confirmShutdown(t *testing.T, coordinator *ShutdownCoordinator, attempt ShutdownAttemptID) {
	t.Helper()
	if phase := coordinator.ConfirmApplication(attempt, true); phase != ShutdownClosingDocuments {
		t.Fatalf("ConfirmApplication() = %v, want document closure", phase)
	}
}

func TestShutdownCoordinatorCancelledConfirmationCannotResume(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{1, 2})
	if !coordinator.Active() || !coordinator.ConfirmationPending() || coordinator.TeardownActive() {
		t.Fatal("confirmation phase was not distinguished from confirmed teardown")
	}
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

func TestAllowsApplicationExitIgnoresMachineAndProgramState(t *testing.T) {
	states := []struct {
		name  string
		state app.State
	}{
		{"disconnected normal", app.State{ConnectionStatus: app.ConnectionDisconnected, MachineReadiness: app.MachineReadinessNormal}},
		{"disconnected recovery", app.State{ConnectionStatus: app.ConnectionDisconnected, MachineReadiness: app.MachineReadinessRecoveryRequired}},
		{"connected normal", app.State{ConnectionStatus: app.ConnectionConnected, MachineReadiness: app.MachineReadinessNormal}},
		{"connected recovery", app.State{ConnectionStatus: app.ConnectionConnected, MachineReadiness: app.MachineReadinessRecoveryRequired}},
		{"emergency stop", app.State{ConnectionStatus: app.ConnectionConnected, EStopStatus: app.EStopActive, MachineReadiness: app.MachineReadinessRecoveryRequired}},
		{"running", app.State{ProgramStatus: app.ProgramRunning, MachineReadiness: app.MachineReadinessRecoveryRequired}},
		{"stopping", app.State{ProgramStatus: app.ProgramStopping, MachineReadiness: app.MachineReadinessStopping}},
		{"failed stop", app.State{ProgramStatus: app.ProgramFailed, MachineReadiness: app.MachineReadinessRecoveryRequired}},
		{"pending admission", app.State{ProgramStatus: app.ProgramLoaded, MachineReadiness: app.MachineReadinessRecoveryRequired}},
		{"unresponsive controller", app.State{ConnectionStatus: app.ConnectionConnected, MachineReadiness: app.MachineReadinessRecoveryRequired}},
	}
	for _, test := range states {
		t.Run(test.name, func(t *testing.T) {
			if !AllowsApplicationExit(test.state, 1) {
				t.Fatal("machine or pending start admission blocked application exit")
			}
		})
	}
}

func TestShutdownCoordinatorConfirmedExitBeginsTeardownWithoutMachineInput(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{7})
	confirmShutdown(t, coordinator, attempt)
	if coordinator.ConfirmationPending() || !coordinator.TeardownActive() {
		t.Fatal("accepted exit did not enter confirmed teardown")
	}
	id, ok := coordinator.NextDocumentToClose(attempt)
	if !ok || id != 7 || !coordinator.AllowsDocumentClose(attempt, id) {
		t.Fatalf("confirmed exit did not authorize document closure: id=%d ok=%v phase=%v", id, ok, coordinator.Phase())
	}
}

func TestShutdownAsyncDispositionPreservesResultsUntilConfirmationOutcome(t *testing.T) {
	tests := []struct {
		name  string
		phase ShutdownPhase
		kind  ShutdownAsyncResultKind
		want  ShutdownAsyncResultDisposition
	}{
		{"idle start", ShutdownIdle, ShutdownAsyncStartResult, ShutdownAsyncApply},
		{"idle action", ShutdownIdle, ShutdownAsyncActionResult, ShutdownAsyncApply},
		{"idle stop", ShutdownIdle, ShutdownAsyncStopAndCloseResult, ShutdownAsyncApply},
		{"confirmation pending start", ShutdownConfirming, ShutdownAsyncStartResult, ShutdownAsyncDefer},
		{"confirmation pending action", ShutdownConfirming, ShutdownAsyncActionResult, ShutdownAsyncApply},
		{"confirmation pending stop", ShutdownConfirming, ShutdownAsyncStopAndCloseResult, ShutdownAsyncDefer},
		{"provisional teardown start", ShutdownClosingDocuments, ShutdownAsyncStartResult, ShutdownAsyncDefer},
		{"provisional teardown action", ShutdownClosingDocuments, ShutdownAsyncActionResult, ShutdownAsyncDefer},
		{"ready-to-commit stop", ShutdownReadyToCloseMain, ShutdownAsyncStopAndCloseResult, ShutdownAsyncDefer},
		{"finished stop", ShutdownFinished, ShutdownAsyncStopAndCloseResult, ShutdownAsyncDiscard},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ShutdownAsyncDisposition(test.phase, test.kind); got != test.want {
				t.Fatalf("ShutdownAsyncDisposition(%v, %v) = %v, want %v", test.phase, test.kind, got, test.want)
			}
		})
	}
}

func TestShutdownCoordinatorAbortedTeardownMakesDeferredResultsRecoverable(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{1, 2})
	confirmShutdown(t, coordinator, attempt)
	first, ok := coordinator.NextDocumentToClose(attempt)
	if !ok || !coordinator.DocumentClosed(attempt, first) {
		t.Fatal("first document did not close during provisional teardown")
	}
	second, ok := coordinator.NextDocumentToClose(attempt)
	if !ok || second == first {
		t.Fatal("second document was not selected after partial teardown")
	}
	for _, kind := range []ShutdownAsyncResultKind{
		ShutdownAsyncStartResult,
		ShutdownAsyncActionResult,
		ShutdownAsyncStopAndCloseResult,
	} {
		if got := ShutdownAsyncDisposition(coordinator.Phase(), kind); got != ShutdownAsyncDefer {
			t.Fatalf("provisional result kind %v = %v, want defer", kind, got)
		}
	}
	if !coordinator.DocumentCloseRejected(attempt, second) || coordinator.Phase() != ShutdownIdle {
		t.Fatalf("failed teardown phase = %v", coordinator.Phase())
	}
	for _, kind := range []ShutdownAsyncResultKind{
		ShutdownAsyncStartResult,
		ShutdownAsyncActionResult,
		ShutdownAsyncStopAndCloseResult,
	} {
		if got := ShutdownAsyncDisposition(coordinator.Phase(), kind); got != ShutdownAsyncApply {
			t.Fatalf("aborted teardown result kind %v = %v, want apply", kind, got)
		}
	}
	if next, ok := coordinator.Begin([]app.DocumentID{second}); !ok || next == attempt {
		t.Fatal("aborted teardown did not permit a fresh exit attempt")
	}
}

func TestShutdownCoordinatorCommittedExitDiscardsDeferredResults(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{1})
	confirmShutdown(t, coordinator, attempt)
	id, ok := coordinator.NextDocumentToClose(attempt)
	if !ok || !coordinator.DocumentClosed(attempt, id) {
		t.Fatal("document close did not reach final commit")
	}
	if _, ok := coordinator.NextDocumentToClose(attempt); ok || !coordinator.CommitMainClose(attempt) || !coordinator.ExitCommitted() {
		t.Fatal("shutdown did not commit final exit")
	}
	for _, kind := range []ShutdownAsyncResultKind{
		ShutdownAsyncStartResult,
		ShutdownAsyncActionResult,
		ShutdownAsyncStopAndCloseResult,
	} {
		if got := ShutdownAsyncDisposition(coordinator.Phase(), kind); got != ShutdownAsyncDiscard {
			t.Fatalf("committed result kind %v = %v, want discard", kind, got)
		}
	}
}

func TestShutdownCoordinatorClosesDocumentsAndMainExactlyOnce(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{7, 8})
	confirmShutdown(t, coordinator, attempt)
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

func TestShutdownCoordinatorRejectsOnlyNativeDocumentCloseFailure(t *testing.T) {
	coordinator, attempt := beginShutdown(t, []app.DocumentID{1, 2})
	confirmShutdown(t, coordinator, attempt)
	id, ok := coordinator.NextDocumentToClose(attempt)
	if !ok || !coordinator.DocumentCloseRejected(attempt, id) || coordinator.Phase() != ShutdownIdle {
		t.Fatalf("rejected close phase = %v", coordinator.Phase())
	}
	if _, ok := coordinator.NextDocumentToClose(attempt); ok || coordinator.DocumentClosed(attempt, 2) {
		t.Fatal("cancelled shutdown accepted later document work")
	}
}

func TestShutdownCoordinatorAuthorizesOnlySelectedDocumentAndRejectsStaleCallbacks(t *testing.T) {
	coordinator, first := beginShutdown(t, []app.DocumentID{1, 2, 3})
	confirmShutdown(t, coordinator, first)
	id, ok := coordinator.NextDocumentToClose(first)
	if !ok || !coordinator.AllowsDocumentClose(first, id) {
		t.Fatal("missing selected document")
	}
	unselected := app.DocumentID(1)
	for _, candidate := range []app.DocumentID{1, 2, 3} {
		if candidate != id {
			unselected = candidate
			break
		}
	}
	if unselected == id {
		t.Fatal("test fixture did not provide an unselected document")
	}
	if coordinator.DocumentClosed(first, 99) || coordinator.DocumentClosed(first, unselected) {
		t.Fatal("stale or unselected callback was accepted")
	}
	if !coordinator.AllowsDocumentClose(first, id) {
		t.Fatal("rejected callback revoked selected document authorization")
	}
	if !coordinator.Cancel(first) {
		t.Fatal("first attempt was not cancelled")
	}
	second, ok := coordinator.Begin([]app.DocumentID{id})
	if !ok {
		t.Fatal("second attempt was not created")
	}
	confirmShutdown(t, coordinator, second)
	if coordinator.AllowsDocumentClose(first, id) {
		t.Fatal("old attempt authorization survived cancellation")
	}
	if coordinator.DocumentClosed(first, id) || coordinator.DocumentCloseRejected(first, id) {
		t.Fatal("stale callback from abandoned attempt affected the new attempt")
	}
	if next, ok := coordinator.NextDocumentToClose(second); !ok || next != id || !coordinator.DocumentClosed(second, id) {
		t.Fatalf("second attempt close = %d, %v", next, ok)
	}
}

func TestShutdownCoordinatorEmptyApplicationCommitsOneMainClose(t *testing.T) {
	coordinator, attempt := beginShutdown(t, nil)
	confirmShutdown(t, coordinator, attempt)
	if _, ok := coordinator.NextDocumentToClose(attempt); ok || coordinator.Phase() != ShutdownReadyToCloseMain {
		t.Fatalf("empty shutdown did not become ready: %v", coordinator.Phase())
	}
	if !coordinator.CommitMainClose(attempt) || coordinator.CommitMainClose(attempt) {
		t.Fatal("empty shutdown main close was not exactly once")
	}
}
