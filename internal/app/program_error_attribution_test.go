package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRejectedDocumentStartsAreGeneralErrors(t *testing.T) {
	controller, fake := newConnectedDocumentProgramController(t)
	activeDocumentID := DocumentID(101)
	activeRunID, err := controller.StartDocumentProgram(
		context.Background(),
		activeDocumentID,
		documentProgram(t, "active.gcode", "G0 X1\n"),
	)
	if err != nil {
		t.Fatalf("StartDocumentProgram(active) error = %v", err)
	}
	waitForLineWrites(t, fake, "G0 X1")
	drainEvents(controller.Events())

	tests := []struct {
		name       string
		documentID DocumentID
	}{
		{name: "competing valid run", documentID: 202},
		{name: "invalid document ID", documentID: 0},
		{name: "invalid program", documentID: 202},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			program := documentProgram(t, "candidate.gcode", "G0 X2\n")
			if test.name == "invalid program" {
				program.Lines = nil
			}
			runID, err := controller.StartDocumentProgram(context.Background(), test.documentID, program)
			if err == nil || runID != 0 {
				t.Fatalf("rejected start = run %d error %v, want zero run and error", runID, err)
			}
			errorEvent := waitForEvent(t, controller.Events(), EventError)
			if errorEvent.ErrorRunID != 0 || errorEvent.ErrorDocumentID != 0 {
				t.Fatalf("general error association = document %d run %d", errorEvent.ErrorDocumentID, errorEvent.ErrorRunID)
			}
			if errorEvent.State.ProgramRunID != activeRunID || errorEvent.State.ProgramDocumentID != activeDocumentID {
				t.Fatalf("general error lost contextual active identity: %+v", errorEvent.State)
			}
			if errorEvent.State.ProgramError != "" {
				t.Fatalf("active ProgramError = %q, want empty", errorEvent.State.ProgramError)
			}
			state := controller.Snapshot()
			if state.ProgramStatus != ProgramRunning || state.ProgramRunID != activeRunID ||
				state.ProgramDocumentID != activeDocumentID || state.ProgramError != "" {
				t.Fatalf("active run changed by rejected start: %+v", state)
			}
		})
	}

	fake.InjectRX("ok")
	completeProgramAfterFreshIdle(t, controller, fake)
}

func TestCompletedRunUnrelatedErrorHasNoRunAssociation(t *testing.T) {
	controller, fake := newConnectedDocumentProgramController(t)
	documentID := DocumentID(303)
	runID, err := controller.StartDocumentProgram(
		context.Background(),
		documentID,
		documentProgram(t, "completed.gcode", "M5\n"),
	)
	if err != nil {
		t.Fatalf("StartDocumentProgram() error = %v", err)
	}
	waitForLineWrites(t, fake, "M5")
	fake.InjectRX("ok")
	completeProgramAfterFreshIdle(t, controller, fake)
	drainEvents(controller.Events())

	if err := controller.SendConsoleLine(context.Background(), "G0 X1\nG0 X2"); !errors.Is(err, ErrInvalidControllerLine) {
		t.Fatalf("SendConsoleLine() error = %v, want %v", err, ErrInvalidControllerLine)
	}
	errorEvent := waitForEvent(t, controller.Events(), EventError)
	if errorEvent.ErrorRunID != 0 || errorEvent.ErrorDocumentID != 0 {
		t.Fatalf("unrelated error association = document %d run %d", errorEvent.ErrorDocumentID, errorEvent.ErrorRunID)
	}
	state := controller.Snapshot()
	if state.ProgramStatus != ProgramCompleted || state.ProgramDocumentID != documentID ||
		state.ProgramRunID != runID || state.ProgramError != "" {
		t.Fatalf("completed run changed by unrelated error: %+v", state)
	}
	if !errors.Is(errorEvent.Err, ErrInvalidControllerLine) || state.LastError != ErrInvalidControllerLine.Error() {
		t.Fatalf("general error reporting lost: event=%v state=%+v", errorEvent.Err, state)
	}
}

func TestProgramFailureErrorAssociationSurvivesGeneralErrorAndNewRun(t *testing.T) {
	controller, fake := newConnectedDocumentProgramController(t)
	firstDocumentID := DocumentID(404)
	firstRunID, err := controller.StartDocumentProgram(
		context.Background(),
		firstDocumentID,
		documentProgram(t, "failure.gcode", "G0 X1\n"),
	)
	if err != nil {
		t.Fatalf("StartDocumentProgram(first) error = %v", err)
	}
	waitForLineWrites(t, fake, "G0 X1")
	fake.InjectRX("error:2")
	failureEvent := waitForEvent(t, controller.Events(), EventError)
	failureReason := "program failed at line 1: error:2"
	if failureEvent.ErrorDocumentID != firstDocumentID || failureEvent.ErrorRunID != firstRunID {
		t.Fatalf("failure association = document %d run %d, want document %d run %d", failureEvent.ErrorDocumentID, failureEvent.ErrorRunID, firstDocumentID, firstRunID)
	}
	failed := failureEvent.State
	if failed.ProgramStatus != ProgramFailed || failed.ProgramDocumentID != firstDocumentID ||
		failed.ProgramRunID != firstRunID || failed.ProgramError != failureReason || failed.LastError != failureReason {
		t.Fatalf("failed state = %+v", failed)
	}
	drainEvents(controller.Events())

	if err := controller.SendConsoleLine(context.Background(), "M5\nM3"); !errors.Is(err, ErrInvalidControllerLine) {
		t.Fatalf("unrelated SendConsoleLine() error = %v", err)
	}
	generalEvent := waitForEvent(t, controller.Events(), EventError)
	if generalEvent.ErrorRunID != 0 || generalEvent.ErrorDocumentID != 0 {
		t.Fatalf("general error association = document %d run %d", generalEvent.ErrorDocumentID, generalEvent.ErrorRunID)
	}
	afterGeneral := controller.Snapshot()
	if afterGeneral.LastError != ErrInvalidControllerLine.Error() || afterGeneral.ProgramError != failureReason ||
		afterGeneral.ProgramRunID != firstRunID || afterGeneral.ProgramStatus != ProgramFailed {
		t.Fatalf("failed attribution after general error = %+v", afterGeneral)
	}
	reconnectAndConfirmReadiness(t, controller, fake)

	secondDocumentID := DocumentID(405)
	secondRunID, err := controller.StartDocumentProgram(
		context.Background(),
		secondDocumentID,
		documentProgram(t, "replacement.gcode", "M5\n"),
	)
	if err != nil {
		t.Fatalf("StartDocumentProgram(second) error = %v", err)
	}
	if secondRunID == firstRunID {
		t.Fatalf("second RunID = %d, want different from %d", secondRunID, firstRunID)
	}
	second := controller.Snapshot()
	if second.ProgramStatus != ProgramRunning || second.ProgramDocumentID != secondDocumentID ||
		second.ProgramRunID != secondRunID || second.ProgramError != "" || second.LastError != "" {
		t.Fatalf("replacement run state = %+v", second)
	}
	if failureEvent.ErrorRunID != firstRunID || failureEvent.ErrorDocumentID != firstDocumentID {
		t.Fatalf("saved failure event changed identity: %+v", failureEvent)
	}
	waitForLineWrites(t, fake, "G0 X1", "M5")
	fake.InjectRX("ok")
	completed := completeProgramAfterFreshIdle(t, controller, fake)
	if completed.ProgramRunID != secondRunID {
		t.Fatalf("completed RunID = %d, want %d", completed.ProgramRunID, secondRunID)
	}
}

func TestProgramErrorAttributionOnTransportDisconnect(t *testing.T) {
	t.Run("active document run", func(t *testing.T) {
		controller, fake := newConnectedDocumentProgramController(t)
		documentID := DocumentID(501)
		runID, err := controller.StartDocumentProgram(
			context.Background(), documentID, documentProgram(t, "disconnect.gcode", "M5\n"),
		)
		if err != nil {
			t.Fatalf("StartDocumentProgram() error = %v", err)
		}
		waitForLineWrites(t, fake, "M5")
		drainEvents(controller.Events())

		fake.InjectDisconnected()
		errorEvent := waitForEvent(t, controller.Events(), EventError)
		if errorEvent.ErrorDocumentID != documentID || errorEvent.ErrorRunID != runID {
			t.Fatalf("disconnect association = document %d run %d", errorEvent.ErrorDocumentID, errorEvent.ErrorRunID)
		}
		if errorEvent.State.ProgramStatus != ProgramFailed || errorEvent.State.ProgramError != ErrTransportDisconnected.Error() ||
			errorEvent.State.LastError != ErrTransportDisconnected.Error() {
			t.Fatalf("disconnect failure state = %+v", errorEvent.State)
		}
	})

	t.Run("no active run preserves prior failure", func(t *testing.T) {
		controller, fake := newConnectedDocumentProgramController(t)
		runID, err := controller.StartDocumentProgram(
			context.Background(), 502, documentProgram(t, "already-failed.gcode", "M5\n"),
		)
		if err != nil {
			t.Fatalf("StartDocumentProgram() error = %v", err)
		}
		waitForLineWrites(t, fake, "M5")
		fake.InjectRX("error:7")
		failureEvent := waitForEvent(t, controller.Events(), EventError)
		priorProgramError := failureEvent.State.ProgramError
		drainEvents(controller.Events())

		fake.InjectDisconnected()
		state := waitForState(t, controller, func(state State) bool { return !state.IsConnected() })
		if state.ProgramStatus != ProgramFailed || state.ProgramRunID != runID || state.ProgramError != priorProgramError {
			t.Fatalf("disconnect contaminated prior failure: %+v", state)
		}
		ensureNoErrorEvent(t, controller.Events(), 100*time.Millisecond)
	})
}

func TestProgramErrorAttributionOnEmergencyStop(t *testing.T) {
	t.Run("active document run", func(t *testing.T) {
		controller, fake := newConnectedDocumentProgramController(t)
		documentID := DocumentID(601)
		runID, err := controller.StartDocumentProgram(
			context.Background(), documentID, documentProgram(t, "estop.gcode", "G0 X1\n"),
		)
		if err != nil {
			t.Fatalf("StartDocumentProgram() error = %v", err)
		}
		waitForLineWrites(t, fake, "G0 X1")
		drainEvents(controller.Events())

		fake.InjectRX("ALARM:50")
		errorEvent := waitForEvent(t, controller.Events(), EventError)
		if !errors.Is(errorEvent.Err, ErrEmergencyStop) {
			t.Fatalf("e-stop event error = %v", errorEvent.Err)
		}
		if errorEvent.ErrorDocumentID != documentID || errorEvent.ErrorRunID != runID {
			t.Fatalf("e-stop association = document %d run %d", errorEvent.ErrorDocumentID, errorEvent.ErrorRunID)
		}
		if errorEvent.State.ProgramStatus != ProgramFailed ||
			!strings.Contains(errorEvent.State.ProgramError, "ALARM:50") ||
			errorEvent.State.ProgramError != errorEvent.State.LastError {
			t.Fatalf("e-stop program failure state = %+v", errorEvent.State)
		}
	})

	t.Run("no active run remains general", func(t *testing.T) {
		controller, fake := newConnectedDocumentProgramController(t)
		documentID := DocumentID(602)
		runID, err := controller.StartDocumentProgram(
			context.Background(), documentID, documentProgram(t, "completed-before-estop.gcode", "M5\n"),
		)
		if err != nil {
			t.Fatalf("StartDocumentProgram() error = %v", err)
		}
		waitForLineWrites(t, fake, "M5")
		fake.InjectRX("ok")
		completeProgramAfterFreshIdle(t, controller, fake)
		drainEvents(controller.Events())

		fake.InjectRX("ALARM:50")
		errorEvent := waitForEvent(t, controller.Events(), EventError)
		if errorEvent.ErrorRunID != 0 || errorEvent.ErrorDocumentID != 0 {
			t.Fatalf("runless e-stop association = document %d run %d", errorEvent.ErrorDocumentID, errorEvent.ErrorRunID)
		}
		state := errorEvent.State
		if state.ProgramStatus != ProgramCompleted || state.ProgramDocumentID != documentID ||
			state.ProgramRunID != runID || state.ProgramError != "" {
			t.Fatalf("runless e-stop contaminated completion: %+v", state)
		}
	})
}

func TestLegacyProgramFailureAttributionAndReload(t *testing.T) {
	controller, fake := newConnectedDocumentProgramController(t)
	if err := controller.LoadProgramFile(writeProgramFile(t, "legacy-failure.gcode", "M5\n")); err != nil {
		t.Fatalf("LoadProgramFile() error = %v", err)
	}
	if err := controller.StartProgram(context.Background()); err != nil {
		t.Fatalf("StartProgram() error = %v", err)
	}
	legacyRunID := controller.Snapshot().ProgramRunID
	if legacyRunID == 0 {
		t.Fatal("legacy RunID = 0")
	}
	waitForLineWrites(t, fake, "M5")
	fake.InjectRX("error:3")
	errorEvent := waitForEvent(t, controller.Events(), EventError)
	if errorEvent.ErrorRunID != legacyRunID || errorEvent.ErrorDocumentID != 0 {
		t.Fatalf("legacy failure association = document %d run %d", errorEvent.ErrorDocumentID, errorEvent.ErrorRunID)
	}
	if errorEvent.State.ProgramError == "" || errorEvent.State.ProgramError != errorEvent.State.LastError {
		t.Fatalf("legacy failure state = %+v", errorEvent.State)
	}

	if err := controller.LoadProgramFile(writeProgramFile(t, "legacy-reload.gcode", "G0 X1\n")); err != nil {
		t.Fatalf("LoadProgramFile(reload) error = %v", err)
	}
	reloaded := controller.Snapshot()
	if reloaded.ProgramStatus != ProgramLoaded || reloaded.ProgramRunID != 0 ||
		reloaded.ProgramDocumentID != 0 || reloaded.ProgramError != "" {
		t.Fatalf("reloaded legacy state = %+v", reloaded)
	}
}

func TestProgramSessionInvariantFailureIsRunScoped(t *testing.T) {
	controller, fake := newConnectedDocumentProgramController(t)
	documentID := DocumentID(701)
	runID, err := controller.StartDocumentProgram(
		context.Background(), documentID, documentProgram(t, "invariant.gcode", "M5\n"),
	)
	if err != nil {
		t.Fatalf("StartDocumentProgram() error = %v", err)
	}
	waitForLineWrites(t, fake, "M5")

	controller.mu.Lock()
	owner := controller.responseOwner
	controller.responseOwner.session = newResponseSession(make(chan string, 1))
	controller.mu.Unlock()
	t.Cleanup(func() {
		if owner.session != nil {
			owner.session.cancel(context.Canceled)
		}
	})
	drainEvents(controller.Events())

	fake.InjectRX("ok")
	errorEvent := waitForEvent(t, controller.Events(), EventError)
	if !errors.Is(errorEvent.Err, ErrCommandSessionInvariant) {
		t.Fatalf("invariant error = %v, want %v", errorEvent.Err, ErrCommandSessionInvariant)
	}
	if errorEvent.ErrorDocumentID != documentID || errorEvent.ErrorRunID != runID {
		t.Fatalf("invariant association = document %d run %d", errorEvent.ErrorDocumentID, errorEvent.ErrorRunID)
	}
	if errorEvent.State.ProgramStatus != ProgramFailed ||
		errorEvent.State.ProgramError != ErrCommandSessionInvariant.Error() {
		t.Fatalf("invariant failure state = %+v", errorEvent.State)
	}
}
