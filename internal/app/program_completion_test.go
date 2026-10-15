package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ianbruene/ddgo/internal/transport"
)

func startSingleLineDocumentRun(t *testing.T) (*Controller, *transport.FakeTransport, RunID) {
	t.Helper()
	controller, fake := newConnectedDocumentProgramController(t)
	runID, err := controller.StartDocumentProgram(context.Background(), 91, documentProgram(t, "drain.gcode", "G1 X1\n"))
	if err != nil {
		t.Fatalf("StartDocumentProgram() error = %v", err)
	}
	waitForLineWrites(t, fake, "G1 X1")
	return controller, fake, runID
}

func enterProgramDrain(t *testing.T, controller *Controller, fake *transport.FakeTransport) State {
	t.Helper()
	fake.InjectRX("ok")
	state := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramDraining })
	waitForFinalDrainStatusRequest(t, controller)
	return state
}

func TestProgramCompletionRequiresFreshIdleAndRetainsOwnership(t *testing.T) {
	controller, fake, runID := startSingleLineDocumentRun(t)

	// This status predates final command completion and cannot satisfy draining.
	fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool { return state.MachineState == "Idle" })
	draining := enterProgramDrain(t, controller, fake)
	if draining.ProgramComplete != draining.ProgramTotal || draining.ProgramRunID != runID || draining.ProgramDocumentID != 91 {
		t.Fatalf("draining state = %+v", draining)
	}
	controller.mu.RLock()
	owner := controller.responseOwner
	activeRun := controller.run
	controller.mu.RUnlock()
	if owner.kind != responseOwnerProgram || owner.session != activeRun.responseSession() {
		t.Fatalf("draining owner = %+v run = %+v", owner, activeRun)
	}
	if _, err := controller.StartDocumentProgram(context.Background(), 92, documentProgram(t, "other.gcode", "M5\n")); err == nil {
		t.Fatal("competing start succeeded during draining")
	}
	if state := controller.Snapshot(); state.ProgramStatus != ProgramDraining || state.ProgramError != "" {
		t.Fatalf("competing start changed draining run: %+v", state)
	}

	fake.InjectRX("<Run|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool { return state.MachineState == "Run" })
	if got := controller.Snapshot().ProgramStatus; got != ProgramDraining {
		t.Fatalf("status after Run = %q, want draining", got)
	}
	fake.InjectRX("<Hold|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool { return state.MachineState == "Hold" })
	if got := controller.Snapshot().ProgramStatus; got != ProgramDraining {
		t.Fatalf("status after Hold = %q, want draining", got)
	}

	fake.InjectRX("<Idle|MPos:0,0,0>")
	completed := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
	if completed.ProgramRunID != runID || completed.ProgramDocumentID != 91 || completed.ProgramComplete != completed.ProgramTotal {
		t.Fatalf("completed state = %+v", completed)
	}
	controller.mu.RLock()
	defer controller.mu.RUnlock()
	if controller.run != nil || controller.responseOwner.kind != responseOwnerNone {
		t.Fatalf("completion retained execution ownership: run=%p owner=%+v", controller.run, controller.responseOwner)
	}
}

func TestProgramPauseResumeDuringFinalDrain(t *testing.T) {
	controller, fake, _ := startSingleLineDocumentRun(t)
	enterProgramDrain(t, controller, fake)

	if err := controller.PauseProgram(context.Background()); err != nil {
		t.Fatalf("PauseProgram() error = %v", err)
	}
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramPaused })
	if got := lineWrites(fake); len(got) != 1 {
		t.Fatalf("program lines after pause = %#v, want one", got)
	}

	// Idle while paused must not silently finish or resume the run.
	fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool {
		return state.MachineState == "Idle" && state.ProgramStatus == ProgramPaused
	})
	statusWrites := countStatusWrites(fake.Written())
	if err := controller.ResumeProgram(context.Background()); err != nil {
		t.Fatalf("ResumeProgram() error = %v", err)
	}
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramDraining })
	waitForStatusWritesAfter(t, fake, statusWrites)
	if got := lineWrites(fake); len(got) != 1 {
		t.Fatalf("program lines after resume = %#v, want no resend", got)
	}
	fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
}

func TestStopDuringFinalDrainPreventsLateCompletion(t *testing.T) {
	controller, fake, runID := startSingleLineDocumentRun(t)
	enterProgramDrain(t, controller, fake)
	if err := controller.StopProgram(context.Background()); err != nil {
		t.Fatalf("StopProgram() error = %v", err)
	}
	stopped := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramStopped })
	if stopped.ProgramRunID != runID {
		t.Fatalf("stopped RunID = %d, want %d", stopped.ProgramRunID, runID)
	}
	fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool {
		return state.MachineState == "Idle" && state.ProgramStatus == ProgramStopped
	})
	if got := lineWrites(fake); len(got) != 1 {
		t.Fatalf("program lines after stop = %#v, want one", got)
	}
}

func TestFinalDrainInterruptionsFailCurrentRun(t *testing.T) {
	t.Run("alarm", func(t *testing.T) {
		controller, fake, runID := startSingleLineDocumentRun(t)
		enterProgramDrain(t, controller, fake)
		fake.InjectRX("ALARM:1")
		failed := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramFailed })
		if failed.ProgramRunID != runID || !strings.Contains(failed.ProgramError, "ALARM:1") {
			t.Fatalf("alarm failure state = %+v", failed)
		}
	})

	t.Run("disconnect", func(t *testing.T) {
		controller, fake, runID := startSingleLineDocumentRun(t)
		enterProgramDrain(t, controller, fake)
		fake.InjectDisconnected()
		failed := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramFailed })
		if failed.ProgramRunID != runID || failed.ProgramError != ErrTransportDisconnected.Error() {
			t.Fatalf("disconnect failure state = %+v", failed)
		}
	})

	t.Run("emergency stop", func(t *testing.T) {
		controller, fake, runID := startSingleLineDocumentRun(t)
		enterProgramDrain(t, controller, fake)
		fake.InjectRX("ALARM:50")
		failed := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramFailed })
		if failed.ProgramRunID != runID || failed.EStopStatus != EStopActive || failed.EStopSource != EStopSourceAlarm50 {
			t.Fatalf("emergency-stop failure state = %+v", failed)
		}
	})

	t.Run("status write", func(t *testing.T) {
		controller, fake, runID := startSingleLineDocumentRun(t)
		wantErr := errors.New("status write failed")
		fake.SetWriteError(wantErr)
		fake.InjectRX("ok")
		failed := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramFailed })
		if failed.ProgramRunID != runID || !strings.Contains(failed.ProgramError, wantErr.Error()) {
			t.Fatalf("status-write failure state = %+v", failed)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		controller, fake := newConnectedDocumentProgramController(t)
		ctx, cancel := context.WithCancel(context.Background())
		runID, err := controller.StartDocumentProgram(ctx, 91, documentProgram(t, "cancel.gcode", "M5\n"))
		if err != nil {
			t.Fatalf("StartDocumentProgram() error = %v", err)
		}
		waitForLineWrites(t, fake, "M5")
		enterProgramDrain(t, controller, fake)
		cancel()
		failed := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramFailed })
		if failed.ProgramRunID != runID || failed.ProgramError != context.Canceled.Error() {
			t.Fatalf("cancellation failure state = %+v", failed)
		}
	})
}

func TestStaleGenerationStatusCannotCompleteFinalDrain(t *testing.T) {
	controller, fake, _ := startSingleLineDocumentRun(t)
	enterProgramDrain(t, controller, fake)
	currentGeneration := fake.Generation()
	fake.InjectRXForGeneration(currentGeneration+1, "<Idle|MPos:0,0,0>")
	fake.InjectRX("stale-generation-barrier")
	waitForEventText(t, controller.Events(), EventConsoleRX, "stale-generation-barrier")
	if got := controller.Snapshot().ProgramStatus; got != ProgramDraining {
		t.Fatalf("stale-generation Idle changed status to %q, want draining", got)
	}
	fake.InjectRXForGeneration(currentGeneration, "<Idle|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
}

func TestStaleDrainingWorkerCannotCompleteReplacementRun(t *testing.T) {
	controller, fake, _ := startSingleLineDocumentRun(t)
	enterProgramDrain(t, controller, fake)
	controller.mu.RLock()
	staleRun := controller.run
	controller.mu.RUnlock()
	if err := controller.StopProgram(context.Background()); err != nil {
		t.Fatalf("StopProgram() error = %v", err)
	}

	replacementID, err := controller.StartDocumentProgram(context.Background(), 92, documentProgram(t, "replacement.gcode", "M5\n"))
	if err != nil {
		t.Fatalf("replacement StartDocumentProgram() error = %v", err)
	}
	if controller.finishProgramSuccess(staleRun) {
		t.Fatal("stale run reported successful completion")
	}
	state := controller.Snapshot()
	if state.ProgramStatus != ProgramRunning || state.ProgramRunID != replacementID || state.ProgramDocumentID != 92 {
		t.Fatalf("stale completion changed replacement state: %+v", state)
	}
	if err := controller.StopProgram(context.Background()); err != nil {
		t.Fatalf("replacement StopProgram() error = %v", err)
	}
}

func TestFinalSystemCommandStillRequiresProgramDrainIdle(t *testing.T) {
	controller, fake := startBarrierTestProgram(t, "$G\n", false)
	waitForStatusWritesAfter(t, fake, 0)
	fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForLineWrites(t, fake, "$G")
	postCommandPolls := countStatusWrites(fake.Written())
	fake.InjectRX("ok")
	waitForStatusWritesAfter(t, fake, postCommandPolls)
	fake.InjectRX("<Idle|MPos:0,0,0>")
	draining := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramDraining })
	if draining.ProgramComplete != 1 {
		t.Fatalf("draining progress = %d, want 1", draining.ProgramComplete)
	}
	waitForFinalDrainStatusRequest(t, controller)
	fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
}
