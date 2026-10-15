package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ianbruene/ddgo/internal/grbl"
	"github.com/ianbruene/ddgo/internal/transport"
)

type blockingPauseTransport struct {
	fake *transport.FakeTransport

	mu          sync.Mutex
	blockHold   bool
	holdStarted chan struct{}
	releaseHold chan struct{}
	holdErr     error
	holdOnce    sync.Once
	holdWrites  int
}

func newBlockingPauseTransport() *blockingPauseTransport {
	return &blockingPauseTransport{fake: transport.NewFakeTransport()}
}

func (t *blockingPauseTransport) Events() <-chan transport.Event { return t.fake.Events() }

func (t *blockingPauseTransport) Open(ctx context.Context, cfg transport.PortConfig) (transport.ConnectionGeneration, error) {
	return t.fake.Open(ctx, cfg)
}

func (t *blockingPauseTransport) Close() error { return t.fake.Close() }

func (t *blockingPauseTransport) Write(ctx context.Context, msg transport.Message) error {
	if msg.Display == "!" {
		t.mu.Lock()
		t.holdWrites++
		block := t.blockHold
		started := t.holdStarted
		release := t.releaseHold
		holdErr := t.holdErr
		if block {
			t.blockHold = false
		}
		t.mu.Unlock()
		if block {
			t.holdOnce.Do(func() { close(started) })
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-release:
			}
			if holdErr != nil {
				return holdErr
			}
		}
	}
	return t.fake.Write(ctx, msg)
}

func (t *blockingPauseTransport) blockNextHold(err error) (<-chan struct{}, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.blockHold = true
	t.holdStarted = make(chan struct{})
	t.releaseHold = make(chan struct{})
	t.holdErr = err
	t.holdOnce = sync.Once{}
	release := t.releaseHold
	return t.holdStarted, func() { close(release) }
}

func (t *blockingPauseTransport) holdWriteCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.holdWrites
}

func newBlockingPauseController(t *testing.T) (*Controller, *blockingPauseTransport) {
	t.Helper()
	tr := newBlockingPauseTransport()
	tr.fake.SetResponding(false)
	controller := NewController(tr, nil)
	controller.statusPollInterval = time.Hour
	if err := controller.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	drainEvents(controller.Events())
	return controller, tr
}

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

func TestOverlappingPauseHasSingleOwnerDuringFinalDrain(t *testing.T) {
	controller, tr := newBlockingPauseController(t)
	runID, err := controller.StartDocumentProgram(context.Background(), 91, documentProgram(t, "pause.gcode", "G1 X1\n"))
	if err != nil {
		t.Fatalf("StartDocumentProgram() error = %v", err)
	}
	waitForLineWrites(t, tr.fake, "G1 X1")
	enterProgramDrain(t, controller, tr.fake)

	holdStarted, releaseHold := tr.blockNextHold(nil)
	pauseDone := make(chan error, 1)
	go func() { pauseDone <- controller.PauseProgram(context.Background()) }()
	<-holdStarted

	controller.mu.RLock()
	run := controller.run
	transition := run.pauseTransition
	owner := controller.responseOwner
	controller.mu.RUnlock()
	if transition == 0 {
		t.Fatal("accepted Pause did not install its transition token")
	}
	if err := controller.PauseProgram(context.Background()); !errors.Is(err, ErrPauseInProgress) {
		t.Fatalf("overlapping Pause error = %v, want %v", err, ErrPauseInProgress)
	}
	if err := controller.ResumeProgram(context.Background()); !errors.Is(err, ErrPauseInProgress) {
		t.Fatalf("Resume during pending Pause error = %v, want %v", err, ErrPauseInProgress)
	}
	controller.mu.RLock()
	if controller.run != run || run.pauseTransition != transition || controller.responseOwner != owner {
		controller.mu.RUnlock()
		t.Fatal("rejected operation changed Pause ownership or response ownership")
	}
	controller.mu.RUnlock()
	if got := tr.holdWriteCount(); got != 1 {
		t.Fatalf("Hold write attempts = %d, want 1", got)
	}

	tr.fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool {
		return state.MachineState == "Idle" && state.ProgramStatus == ProgramDraining
	})
	releaseHold()
	if err := <-pauseDone; err != nil {
		t.Fatalf("owning Pause error = %v", err)
	}
	paused := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramPaused })
	if paused.ProgramRunID != runID || paused.ProgramDocumentID != 91 {
		t.Fatalf("paused identity = %+v", paused)
	}
	controller.mu.RLock()
	if controller.run != run || run.pauseTransition != 0 || controller.responseOwner != owner {
		controller.mu.RUnlock()
		t.Fatal("successful Pause lost run ownership or retained its transition token")
	}
	controller.mu.RUnlock()

	statusWrites := countStatusWrites(tr.fake.Written())
	if err := controller.ResumeProgram(context.Background()); err != nil {
		t.Fatalf("ResumeProgram() error = %v", err)
	}
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramDraining })
	waitForStatusWritesAfter(t, tr.fake, statusWrites)
	if got := lineWrites(tr.fake); len(got) != 1 {
		t.Fatalf("program lines after Pause/Resume = %#v, want no resend", got)
	}
	tr.fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
}

func TestFailedPauseKeepsInterlockUntilOwnerFinishes(t *testing.T) {
	controller, tr := newBlockingPauseController(t)
	_, err := controller.StartDocumentProgram(context.Background(), 91, documentProgram(t, "pause-failure.gcode", "G1 X1\n"))
	if err != nil {
		t.Fatalf("StartDocumentProgram() error = %v", err)
	}
	waitForLineWrites(t, tr.fake, "G1 X1")
	enterProgramDrain(t, controller, tr.fake)

	wantErr := errors.New("Hold write failed")
	holdStarted, releaseHold := tr.blockNextHold(wantErr)
	pauseDone := make(chan error, 1)
	go func() { pauseDone <- controller.PauseProgram(context.Background()) }()
	<-holdStarted
	controller.mu.RLock()
	run := controller.run
	transition := run.pauseTransition
	controller.mu.RUnlock()
	if err := controller.PauseProgram(context.Background()); !errors.Is(err, ErrPauseInProgress) {
		t.Fatalf("overlapping Pause error = %v, want %v", err, ErrPauseInProgress)
	}
	controller.mu.RLock()
	if run.pauseTransition != transition || transition == 0 {
		controller.mu.RUnlock()
		t.Fatal("rejected Pause cleared the owning transition")
	}
	controller.mu.RUnlock()

	tr.fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool {
		return state.MachineState == "Idle" && state.ProgramStatus == ProgramDraining
	})
	statusWrites := countStatusWrites(tr.fake.Written())
	releaseHold()
	if err := <-pauseDone; !errors.Is(err, wantErr) {
		t.Fatalf("owning Pause error = %v, want %v", err, wantErr)
	}
	controller.mu.RLock()
	transition = run.pauseTransition
	controller.mu.RUnlock()
	if transition != 0 {
		t.Fatalf("failed owning Pause retained transition %d", transition)
	}
	if state := controller.Snapshot(); state.ProgramStatus != ProgramDraining || state.ProgramDocumentID != 91 {
		t.Fatalf("state after failed Pause = %+v", state)
	}

	// The Idle observed while Pause owned the interlock was invalidated. The
	// drain worker must issue and satisfy a new status synchronization.
	waitForStatusWritesAfter(t, tr.fake, statusWrites)
	if got := controller.Snapshot().ProgramStatus; got != ProgramDraining {
		t.Fatalf("status before replacement Idle = %q, want draining", got)
	}
	tr.fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
}

func TestOverlappingPauseWhileRunningDoesNotAdvanceProgram(t *testing.T) {
	controller, tr := newBlockingPauseController(t)
	_, err := controller.StartDocumentProgram(context.Background(), 91, documentProgram(t, "running-pause.gcode", "G1 X1\nG1 X2\n"))
	if err != nil {
		t.Fatalf("StartDocumentProgram() error = %v", err)
	}
	waitForLineWrites(t, tr.fake, "G1 X1")
	holdStarted, releaseHold := tr.blockNextHold(nil)
	pauseDone := make(chan error, 1)
	go func() { pauseDone <- controller.PauseProgram(context.Background()) }()
	<-holdStarted
	if err := controller.PauseProgram(context.Background()); !errors.Is(err, ErrPauseInProgress) {
		t.Fatalf("overlapping Pause error = %v, want %v", err, ErrPauseInProgress)
	}
	tr.fake.InjectRX("ok")
	waitForState(t, controller, func(state State) bool { return state.ProgramComplete == 1 })
	if got := lineWrites(tr.fake); len(got) != 1 {
		t.Fatalf("program advanced during pending Pause: %#v", got)
	}
	releaseHold()
	if err := <-pauseDone; err != nil {
		t.Fatalf("owning Pause error = %v", err)
	}
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramPaused })
	if err := controller.ResumeProgram(context.Background()); err != nil {
		t.Fatalf("ResumeProgram() error = %v", err)
	}
	waitForLineWrites(t, tr.fake, "G1 X1", "G1 X2")
	if got := tr.holdWriteCount(); got != 1 {
		t.Fatalf("Hold write attempts = %d, want 1", got)
	}
	stopProgramAndConfirm(t, controller, tr.fake)
}

func TestPauseRealtimeReservationSerializesStop(t *testing.T) {
	controller, tr := newBlockingPauseController(t)
	_, err := controller.StartDocumentProgram(context.Background(), 91, documentProgram(t, "pause-stop.gcode", "G1 X1\n"))
	if err != nil {
		t.Fatalf("StartDocumentProgram() error = %v", err)
	}
	waitForLineWrites(t, tr.fake, "G1 X1")
	enterProgramDrain(t, controller, tr.fake)
	holdStarted, releaseHold := tr.blockNextHold(nil)
	pauseDone := make(chan error, 1)
	go func() { pauseDone <- controller.PauseProgram(context.Background()) }()
	<-holdStarted
	statusBefore := 0
	for _, msg := range tr.fake.Written() {
		if msg.Display == "?" {
			statusBefore++
		}
	}
	stopDone := make(chan error, 1)
	go func() { stopDone <- controller.StopProgram(context.Background()) }()
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramStopping })
	releaseHold()
	if err := <-pauseDone; err != nil {
		t.Fatalf("PauseProgram() error = %v", err)
	}
	waitForLineWrites(t, tr.fake, "G1 X1")
	deadline := time.Now().Add(2 * time.Second)
	for {
		writes := nonStatusWrites(tr.fake)
		if len(writes) >= 4 && writes[len(writes)-1].Display == "Ctrl-X" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for Stop after Pause reservation")
		}
		time.Sleep(time.Millisecond)
	}
	tr.fake.InjectRX("Grbl 1.1g [help:'$']")
	waitForStatusWritesAfter(t, tr.fake, statusBefore)
	tr.fake.InjectRX("<Idle|MPos:0,0,0>")
	if err := <-stopDone; err != nil {
		t.Fatalf("StopProgram() error = %v", err)
	}
	writes := nonStatusWrites(tr.fake)
	if len(writes) < 4 || writes[len(writes)-2].Display != "!" || writes[len(writes)-1].Display != "Ctrl-X" {
		t.Fatalf("final Stop writes = %#v, want Hold then Ctrl-X", writes)
	}
}

func TestStopWinningBeforePauseWritePreventsLateHold(t *testing.T) {
	controller, tr := newBlockingPauseController(t)
	_, err := controller.StartDocumentProgram(context.Background(), 91, documentProgram(t, "stale-pause.gcode", "G1 X1\n"))
	if err != nil {
		t.Fatalf("StartDocumentProgram() error = %v", err)
	}
	waitForLineWrites(t, tr.fake, "G1 X1")
	enterProgramDrain(t, controller, tr.fake)
	controller.mu.Lock()
	staleRun, transition, err := controller.beginPauseTransitionLocked()
	controller.mu.Unlock()
	if err != nil {
		t.Fatalf("beginPauseTransitionLocked() error = %v", err)
	}
	stopProgramAndConfirm(t, controller, tr.fake)
	replacementID, err := controller.StartDocumentProgram(context.Background(), 92, documentProgram(t, "replacement.gcode", "M5\n"))
	if err != nil {
		t.Fatalf("replacement StartDocumentProgram() error = %v", err)
	}
	holdsBefore := tr.holdWriteCount()
	if err := controller.writeProgramActionForRun(context.Background(), staleRun, transition, grbl.ActionHold); !errors.Is(err, ErrCommandSessionCanceled) {
		t.Fatalf("stale Pause write error = %v, want %v", err, ErrCommandSessionCanceled)
	}
	controller.mu.Lock()
	controller.endPauseTransitionLocked(staleRun, transition)
	controller.mu.Unlock()
	if got := tr.holdWriteCount(); got != holdsBefore {
		t.Fatalf("late Pause added Hold attempt: got %d, want %d", got, holdsBefore)
	}
	state := controller.Snapshot()
	if state.ProgramStatus != ProgramRunning || state.ProgramRunID != replacementID || state.ProgramDocumentID != 92 {
		t.Fatalf("late Pause changed replacement run: %+v", state)
	}
	stopProgramAndConfirm(t, controller, tr.fake)
}

func TestPendingPauseCannotResurrectInterruptedRun(t *testing.T) {
	tests := []struct {
		name      string
		interrupt func(*transport.FakeTransport)
		wantEStop bool
	}{
		{name: "disconnect", interrupt: func(fake *transport.FakeTransport) { fake.InjectDisconnected() }},
		{name: "emergency stop", interrupt: func(fake *transport.FakeTransport) { fake.InjectRX("ALARM:50") }, wantEStop: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller, tr := newBlockingPauseController(t)
			runID, err := controller.StartDocumentProgram(context.Background(), 91, documentProgram(t, "interrupted-pause.gcode", "G1 X1\n"))
			if err != nil {
				t.Fatalf("StartDocumentProgram() error = %v", err)
			}
			waitForLineWrites(t, tr.fake, "G1 X1")
			enterProgramDrain(t, controller, tr.fake)
			holdStarted, releaseHold := tr.blockNextHold(nil)
			pauseDone := make(chan error, 1)
			go func() { pauseDone <- controller.PauseProgram(context.Background()) }()
			<-holdStarted
			controller.mu.RLock()
			staleRun := controller.run
			controller.mu.RUnlock()
			tt.interrupt(tr.fake)
			failed := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramFailed })
			if failed.ProgramRunID != runID || (tt.wantEStop && failed.EStopStatus != EStopActive) {
				t.Fatalf("interrupted state = %+v", failed)
			}
			releaseHold()
			<-pauseDone
			controller.mu.RLock()
			transition := staleRun.pauseTransition
			controller.mu.RUnlock()
			if transition != 0 {
				t.Fatalf("interrupted Pause retained transition %d", transition)
			}
			if state := controller.Snapshot(); state.ProgramStatus != ProgramFailed || state.ProgramRunID != runID {
				t.Fatalf("late Pause result changed terminal state: %+v", state)
			}
		})
	}
}

func TestStopDuringFinalDrainPreventsLateCompletion(t *testing.T) {
	controller, fake, runID := startSingleLineDocumentRun(t)
	enterProgramDrain(t, controller, fake)
	stopProgramAndConfirm(t, controller, fake)
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
	stopProgramAndConfirm(t, controller, fake)

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
	stopProgramAndConfirm(t, controller, fake)
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
