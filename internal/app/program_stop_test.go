package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ianbruene/ddgo/internal/transport"
)

type selectiveWriteFailureTransport struct {
	fake        *transport.FakeTransport
	failDisplay string
	failErr     error
}

type blockingStopWriteTransport struct {
	fake    *transport.FakeTransport
	started chan struct{}
	release chan struct{}
}

func (t *blockingStopWriteTransport) Open(ctx context.Context, cfg transport.PortConfig) (transport.ConnectionGeneration, error) {
	return t.fake.Open(ctx, cfg)
}
func (t *blockingStopWriteTransport) Close() error                   { return t.fake.Close() }
func (t *blockingStopWriteTransport) Events() <-chan transport.Event { return t.fake.Events() }
func (t *blockingStopWriteTransport) Write(ctx context.Context, msg transport.Message) error {
	if msg.Display == "!" {
		select {
		case <-t.started:
		default:
			close(t.started)
		}
		<-t.release // Deliberately models an OS write that ignores cancellation.
	}
	return t.fake.Write(ctx, msg)
}

func (t *selectiveWriteFailureTransport) Open(ctx context.Context, cfg transport.PortConfig) (transport.ConnectionGeneration, error) {
	return t.fake.Open(ctx, cfg)
}
func (t *selectiveWriteFailureTransport) Close() error                   { return t.fake.Close() }
func (t *selectiveWriteFailureTransport) Events() <-chan transport.Event { return t.fake.Events() }
func (t *selectiveWriteFailureTransport) Write(ctx context.Context, msg transport.Message) error {
	if msg.Display == t.failDisplay {
		return t.failErr
	}
	return t.fake.Write(ctx, msg)
}

func waitForDisplayWrite(t *testing.T, fake *transport.FakeTransport, display string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, msg := range fake.Written() {
			if msg.Display == display {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q write", display)
}

func TestStopRequiresResetBannerAndFreshIdleBeforeStopped(t *testing.T) {
	controller, fake, runID := startSingleLineDocumentRun(t)
	fake.SetResponding(false)
	statusBefore := countStatusWrites(fake.Written())
	done := make(chan error, 1)
	go func() { done <- controller.StopProgram(context.Background()) }()
	stopping := waitForEventText(t, controller.Events(), EventStateChanged, "program stopping").State
	if stopping.MachineReadiness != MachineReadinessStopping || stopping.ProgramRunID != runID {
		t.Fatalf("stopping state = %+v", stopping)
	}
	fake.InjectRX("ok")
	waitForEventText(t, controller.Events(), EventConsoleRX, "ok")
	if state := controller.Snapshot(); state.ProgramStatus != ProgramStopping {
		t.Fatalf("late terminal response changed Stop lifecycle: %+v", state)
	}
	if _, err := controller.StartDocumentProgram(context.Background(), 92, documentProgram(t, "blocked.gcode", "M5\n")); !errors.Is(err, ErrMachineNotReady) {
		t.Fatalf("start during Stop error = %v, want %v", err, ErrMachineNotReady)
	}
	if err := controller.SendConsoleLine(context.Background(), "M5"); !errors.Is(err, ErrMachineNotReady) {
		t.Fatalf("manual command during Stop error = %v, want %v", err, ErrMachineNotReady)
	}
	waitForDisplayWrite(t, fake, "Ctrl-X")
	fake.InjectRX("<Idle|MPos:0,0,0>") // Predates the reset barrier.
	if state := controller.Snapshot(); state.ProgramStatus == ProgramStopped {
		t.Fatalf("stale Idle finalized Stop: %+v", state)
	}
	fake.InjectRX("Grbl 1.1g [help:'$']")
	waitForStatusWritesAfter(t, fake, statusBefore)
	fake.InjectRX("<Run|MPos:0,0,0>")
	if state := controller.Snapshot(); state.ProgramStatus == ProgramStopped {
		t.Fatalf("fresh Run finalized Stop: %+v", state)
	}
	fake.InjectRX("<Idle|MPos:0,0,0>")
	if err := <-done; err != nil {
		t.Fatalf("StopProgram() error = %v", err)
	}
	stopped := controller.Snapshot()
	if stopped.ProgramStatus != ProgramStopped || stopped.MachineReadiness != MachineReadinessNormal || stopped.ProgramRunID != runID {
		t.Fatalf("final stopped state = %+v", stopped)
	}
	writes := nonStatusWrites(fake)
	if len(writes) < 3 || writes[len(writes)-2].Display != "!" || writes[len(writes)-1].Display != "Ctrl-X" {
		t.Fatalf("Stop writes = %#v", writes)
	}
}

func TestRepeatedStopHasSingleAbortOwner(t *testing.T) {
	controller, fake, _ := startSingleLineDocumentRun(t)
	fake.SetResponding(false)
	done := make(chan error, 1)
	go func() { done <- controller.StopProgram(context.Background()) }()
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramStopping })
	if err := controller.StopProgram(context.Background()); !errors.Is(err, ErrStopInProgress) {
		t.Fatalf("second StopProgram() error = %v, want %v", err, ErrStopInProgress)
	}
	waitForDisplayWrite(t, fake, "Ctrl-X")
	before := countStatusWrites(fake.Written())
	fake.InjectRX("Grbl 1.1g [help:'$']")
	waitForStatusWritesAfter(t, fake, before)
	fake.InjectRX("<Idle|MPos:0,0,0>")
	if err := <-done; err != nil {
		t.Fatalf("owning StopProgram() error = %v", err)
	}
	holds, resets := 0, 0
	for _, msg := range nonStatusWrites(fake) {
		if msg.Display == "!" {
			holds++
		}
		if msg.Display == "Ctrl-X" {
			resets++
		}
	}
	if holds != 1 || resets != 1 {
		t.Fatalf("abort writes: Hold=%d Reset=%d", holds, resets)
	}
}

func TestStopCallerCancellationDoesNotAbandonAbort(t *testing.T) {
	controller, fake, _ := startSingleLineDocumentRun(t)
	fake.SetResponding(false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.StopProgram(ctx) }()
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramStopping })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled StopProgram() error = %v", err)
	}
	waitForDisplayWrite(t, fake, "Ctrl-X")
	before := countStatusWrites(fake.Written())
	fake.InjectRX("Grbl 1.1g [help:'$']")
	waitForStatusWritesAfter(t, fake, before)
	fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForState(t, controller, func(state State) bool {
		return state.ProgramStatus == ProgramStopped && state.MachineReadiness == MachineReadinessNormal
	})
}

func TestStopFailureRequiresRecovery(t *testing.T) {
	for _, display := range []string{"!", "Ctrl-X"} {
		t.Run(display, func(t *testing.T) {
			fake := transport.NewFakeTransport()
			tr := &selectiveWriteFailureTransport{fake: fake, failDisplay: display, failErr: errors.New("forced write failure")}
			controller := NewController(tr, nil)
			controller.statusPollInterval = time.Hour
			if err := controller.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
				t.Fatalf("Connect() error = %v", err)
			}
			_, err := controller.StartDocumentProgram(context.Background(), 91, documentProgram(t, "failure.gcode", "M5\n"))
			if err != nil {
				t.Fatalf("StartDocumentProgram() error = %v", err)
			}
			waitForLineWrites(t, fake, "M5")
			err = controller.StopProgram(context.Background())
			if err == nil {
				t.Fatal("StopProgram() error = nil")
			}
			state := controller.Snapshot()
			if state.ProgramStatus != ProgramFailed || state.MachineReadiness != MachineReadinessRecoveryRequired || state.ProgramError == "" {
				t.Fatalf("failed Stop state = %+v", state)
			}
			if _, err := controller.StartDocumentProgram(context.Background(), 92, documentProgram(t, "blocked.gcode", "M5\n")); !errors.Is(err, ErrMachineNotReady) {
				t.Fatalf("start after failed Stop error = %v", err)
			}
		})
	}
}

func TestBlockedStopWriteTimesOutFailClosedAndRecoveryDisconnectReturns(t *testing.T) {
	fake := transport.NewFakeTransport()
	tr := &blockingStopWriteTransport{fake: fake, started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-tr.release:
		default:
			close(tr.release)
		}
	})
	controller := NewController(tr, nil)
	controller.statusPollInterval = time.Hour
	controller.stopTransportTimeout = 25 * time.Millisecond
	if err := controller.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	_, err := controller.StartDocumentProgram(context.Background(), 91, documentProgram(t, "blocked-write.gcode", "M5\n"))
	if err != nil {
		t.Fatalf("StartDocumentProgram() error = %v", err)
	}
	waitForLineWrites(t, fake, "M5")
	done := make(chan error, 1)
	go func() { done <- controller.StopProgram(context.Background()) }()
	<-tr.started
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("StopProgram() error = nil")
		}
	case <-time.After(time.Second):
		t.Fatal("StopProgram() did not honor bounded transport wait")
	}
	state := controller.Snapshot()
	if state.MachineReadiness != MachineReadinessRecoveryRequired || state.ProgramStatus != ProgramFailed {
		t.Fatalf("blocked-write state = %+v", state)
	}
	disconnected := make(chan error, 1)
	go func() { disconnected <- controller.Disconnect() }()
	select {
	case err := <-disconnected:
		if err != nil {
			t.Fatalf("Disconnect() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("recovery Disconnect() waited on the stuck old-generation write")
	}
	controller.mu.RLock()
	writeActive := controller.realtimeWriteActive
	controller.mu.RUnlock()
	if writeActive || controller.Snapshot().MachineReadiness != MachineReadinessRecoveryRequired {
		t.Fatalf("disconnect did not retain recovery and retire reservation: active=%t state=%+v", writeActive, controller.Snapshot())
	}
	close(tr.release)
}

func TestMissingResetEvidenceFailsClosedAndReconnectRecovers(t *testing.T) {
	controller, fake, _ := startSingleLineDocumentRun(t)
	fake.SetResponding(false)
	controller.stopResetBannerTimeout = 25 * time.Millisecond
	err := controller.StopProgram(context.Background())
	if !errors.Is(err, errResetBannerNotObserved) {
		t.Fatalf("StopProgram() error = %v, want reset-banner failure", err)
	}
	state := controller.Snapshot()
	if state.MachineReadiness != MachineReadinessRecoveryRequired || state.ProgramStatus != ProgramFailed {
		t.Fatalf("unconfirmed Stop state = %+v", state)
	}
	reconnectAndConfirmReadiness(t, controller, fake)
	if state := controller.Snapshot(); state.MachineReadiness != MachineReadinessNormal || state.ProgramStatus != ProgramFailed {
		t.Fatalf("recovered state = %+v", state)
	}
}

func TestMissingPostResetIdleFailsClosed(t *testing.T) {
	controller, fake, _ := startSingleLineDocumentRun(t)
	fake.SetResponding(false)
	controller.stopIdleTimeout = 25 * time.Millisecond
	before := countStatusWrites(fake.Written())
	done := make(chan error, 1)
	go func() { done <- controller.StopProgram(context.Background()) }()
	waitForDisplayWrite(t, fake, "Ctrl-X")
	fake.InjectRX("Grbl 1.1g [help:'$']")
	waitForStatusWritesAfter(t, fake, before)
	err := <-done
	if !errors.Is(err, errPostResetIdleNotObserved) {
		t.Fatalf("StopProgram() error = %v, want post-reset Idle failure", err)
	}
	state := controller.Snapshot()
	if state.ProgramStatus != ProgramFailed || state.MachineReadiness != MachineReadinessRecoveryRequired {
		t.Fatalf("missing-Idle state = %+v", state)
	}
}

func TestPostResetAlarmFailsClosed(t *testing.T) {
	controller, fake, _ := startSingleLineDocumentRun(t)
	fake.SetResponding(false)
	before := countStatusWrites(fake.Written())
	done := make(chan error, 1)
	go func() { done <- controller.StopProgram(context.Background()) }()
	waitForDisplayWrite(t, fake, "Ctrl-X")
	fake.InjectRX("Grbl 1.1g [help:'$']")
	waitForStatusWritesAfter(t, fake, before)
	fake.InjectRX("<Alarm|MPos:0,0,0>")
	if err := <-done; !errors.Is(err, errPostResetIdleNotObserved) {
		t.Fatalf("StopProgram() error = %v, want post-reset verification failure", err)
	}
	state := controller.Snapshot()
	if state.ProgramStatus != ProgramFailed || state.MachineReadiness != MachineReadinessRecoveryRequired {
		t.Fatalf("post-reset Alarm state = %+v", state)
	}
}

func TestProgramFailureBlocksUnsafeAdmissionUntilValidatedReconnect(t *testing.T) {
	controller, fake, _ := startSingleLineDocumentRun(t)
	fake.InjectRX("error:7")
	failed := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramFailed })
	if failed.MachineReadiness != MachineReadinessRecoveryRequired || failed.ProgramError == "" {
		t.Fatalf("failed state = %+v", failed)
	}
	if err := controller.Jog(context.Background(), "X", 1, 100); !errors.Is(err, ErrMachineNotReady) {
		t.Fatalf("Jog() after program failure error = %v", err)
	}
	reconnectAndConfirmReadiness(t, controller, fake)
	if err := controller.SendConsoleLine(context.Background(), "M5"); err != nil {
		t.Fatalf("manual command after recovery error = %v", err)
	}
}
