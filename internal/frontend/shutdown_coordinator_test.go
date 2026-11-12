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

func TestShutdownCoordinatorConfirmedExitIgnoresMachineAndProgramState(t *testing.T) {
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
	}
	for _, test := range states {
		t.Run(test.name, func(t *testing.T) {
			// ShutdownCoordinator intentionally accepts no controller state. This
			// declaration documents the states that must not affect an accepted
			// application exit.
			_ = test.state
			coordinator, attempt := beginShutdown(t, []app.DocumentID{7})
			confirmShutdown(t, coordinator, attempt)
			id, ok := coordinator.NextDocumentToClose(attempt)
			if !ok || id != 7 || !coordinator.AllowsDocumentClose(attempt, id) {
				t.Fatalf("machine/program state blocked document closure: id=%d ok=%v phase=%v", id, ok, coordinator.Phase())
			}
		})
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
	if coordinator.DocumentClosed(first, 99) || coordinator.DocumentClosed(first, 2) {
		t.Fatal("stale or unselected callback was accepted")
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
