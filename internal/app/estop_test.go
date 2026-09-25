package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ianbruene/ddgo/internal/grbl"
	"github.com/ianbruene/ddgo/internal/transport"
)

const (
	testStatusPollInterval = 10 * time.Millisecond
	testResponseTimeout    = 70 * time.Millisecond
)

func connectWatchdogController(t *testing.T, responding bool) (*Controller, *transport.FakeTransport) {
	t.Helper()
	fake := transport.NewFakeTransport()
	fake.SetResponding(responding)
	c := NewController(fake, nil)
	c.statusPollInterval = testStatusPollInterval
	c.controllerResponseTimeout = testResponseTimeout
	if err := c.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	waitForState(t, c, func(s State) bool { return s.ConnectionStatus == ConnectionConnected })
	if responding {
		waitForState(t, c, func(s State) bool { return s.MachineState == "Idle" })
	} else {
		waitForStatusQueryWrite(t, fake, time.Second)
	}
	drainEvents(c.Events())
	return c, fake
}

func waitForEStop(t *testing.T, c *Controller, source EStopSource) State {
	t.Helper()
	return waitForState(t, c, func(s State) bool {
		return s.EStopStatus == EStopActive && s.EStopSource == source
	})
}

func waitForOutstandingHeartbeat(t *testing.T, c *Controller) time.Time {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		started := c.statusWatchdogStartedAt
		c.mu.RUnlock()
		if !started.IsZero() {
			time.Sleep(2 * time.Millisecond)
			c.mu.RLock()
			stillOutstanding := c.statusWatchdogStartedAt == started
			c.mu.RUnlock()
			if stillOutstanding {
				return started
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for an outstanding status heartbeat")
	return time.Time{}
}

func TestStatusWatchdogHealthyControllerDoesNotFire(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	time.Sleep(3 * testResponseTimeout)
	state := c.Snapshot()
	if state.ConnectionStatus != ConnectionConnected || state.EStopStatus != EStopClear {
		t.Fatalf("healthy state = %+v", state)
	}
	if !fake.IsOpen() {
		t.Fatal("transport unexpectedly closed")
	}
}

func TestStatusWatchdogWaitsUntilAPollIsIssued(t *testing.T) {
	fake := transport.NewFakeTransport()
	fake.SetResponding(false)
	c := NewController(fake, nil)
	c.statusPollInterval = 80 * time.Millisecond
	c.controllerResponseTimeout = 30 * time.Millisecond
	if err := c.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	c.mu.Lock()
	if err := c.beginRealtimeWriteLocked(); err != nil {
		c.mu.Unlock()
		t.Fatalf("beginRealtimeWriteLocked() error = %v", err)
	}
	c.mu.Unlock()
	time.Sleep(2 * c.statusPollInterval)
	if state := c.Snapshot(); state.EStopStatus != EStopClear {
		t.Fatalf("watchdog fired before a status poll was issued: %+v", state)
	}
	if got := countStatusWrites(fake.Written()); got != 0 {
		t.Fatalf("status writes while realtime I/O reserved = %d, want 0", got)
	}
	c.endRealtimeWrite()
	waitForEStop(t, c, EStopSourceUnresponsive)
}

func TestStatusWatchdogSilentControllerEntersEStopAndKeepsPolling(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	before := countStatusWrites(fake.Written())
	fake.SetResponding(false)
	state := waitForEStop(t, c, EStopSourceUnresponsive)
	if state.ConnectionStatus != ConnectionConnected || !fake.IsOpen() {
		t.Fatalf("silent controller was disconnected: state=%+v open=%v", state, fake.IsOpen())
	}
	if !strings.Contains(state.LastError, "controller stopped responding") {
		t.Fatalf("LastError = %q", state.LastError)
	}
	waitForStatusWritesAfter(t, fake, before+1)
	if got := countStatusWrites(fake.Written()); got < before+2 {
		t.Fatalf("status writes = %d, want at least %d", got, before+2)
	}

	// The transition is latched; continued missed probes do not emit errors.
	_ = waitForEvent(t, c.Events(), EventStateChanged)
	_ = waitForEvent(t, c.Events(), EventError)
	for _, ev := range collectEventsFor(c.Events(), 3*testResponseTimeout) {
		if ev.Kind == EventError && errors.Is(ev.Err, ErrEmergencyStop) {
			t.Fatalf("duplicate emergency-stop error event: %+v", ev)
		}
	}
}

func TestStatusWatchdogWriteFailuresEnterEStopWithoutDisconnect(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	const timeout = 120 * time.Millisecond
	c.mu.Lock()
	c.controllerResponseTimeout = timeout
	c.mu.Unlock()
	fake.SetWriteError(errors.New("status write failed"))
	started := waitForOutstandingHeartbeat(t, c)

	time.Sleep(timeout / 3)
	if state := c.Snapshot(); state.EStopStatus != EStopClear {
		t.Fatalf("write error immediately entered e-stop: %+v", state)
	}
	state := waitForEStop(t, c, EStopSourceUnresponsive)
	if elapsed := time.Since(started); elapsed < timeout {
		t.Fatalf("watchdog fired after %v, before timeout %v", elapsed, timeout)
	}
	if state.ConnectionStatus != ConnectionConnected || !fake.IsOpen() {
		t.Fatalf("write failure disconnected transport: state=%+v open=%v", state, fake.IsOpen())
	}
	wantLastError := state.LastError
	events := collectEventsFor(c.Events(), 6*testStatusPollInterval)
	emergencyStopErrors := 0
	for _, event := range events {
		if event.Kind == EventError {
			if !errors.Is(event.Err, ErrEmergencyStop) {
				t.Fatalf("quiet heartbeat write surfaced raw error: %+v", event)
			}
			emergencyStopErrors++
		}
	}
	if emergencyStopErrors != 1 {
		t.Fatalf("emergency-stop error events = %d, want 1; events=%+v", emergencyStopErrors, events)
	}
	if got := c.Snapshot().LastError; got != wantLastError || !strings.Contains(got, "emergency stop: controller stopped responding") {
		t.Fatalf("LastError after continued failed polls = %q, want %q", got, wantLastError)
	}
}

func TestStatusWatchdogTransientWriteFailureRecoversBeforeDeadline(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	const timeout = 160 * time.Millisecond
	c.mu.Lock()
	c.controllerResponseTimeout = timeout
	c.mu.Unlock()
	fake.SetWriteError(errors.New("temporary status write failure"))
	started := waitForOutstandingHeartbeat(t, c)
	time.Sleep(2 * testStatusPollInterval)
	fake.SetWriteError(nil)

	waitForState(t, c, func(s State) bool {
		c.mu.RLock()
		responded := c.lastStatusResponseTime.After(started) && c.statusWatchdogStartedAt.IsZero()
		c.mu.RUnlock()
		return responded && s.EStopStatus == EStopClear
	})
	if remaining := time.Until(started.Add(timeout + 40*time.Millisecond)); remaining > 0 {
		time.Sleep(remaining)
	}
	state := c.Snapshot()
	if state.ConnectionStatus != ConnectionConnected || state.EStopStatus != EStopClear || state.LastError != "" {
		t.Fatalf("transient write failure latched e-stop: %+v", state)
	}
}

func TestControllerTransportErrorSuppressLogPolicy(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	generation := fake.Generation()
	originalLastError := c.Snapshot().LastError
	quietErr := errors.New("quiet heartbeat write failed")
	fake.InjectErrorWithSuppressLogForGeneration(generation, quietErr, true)
	for _, event := range collectEventsFor(c.Events(), noExtraLifecycleEventWindow) {
		if event.Kind == EventError && errors.Is(event.Err, quietErr) {
			t.Fatalf("suppressed transport error became app error: %+v", event)
		}
	}
	if got := c.Snapshot().LastError; got != originalLastError {
		t.Fatalf("LastError after suppressed transport error = %q, want %q", got, originalLastError)
	}

	visibleErr := errors.New("manual write failed")
	fake.InjectErrorWithSuppressLogForGeneration(generation, visibleErr, false)
	event := waitForEvent(t, c.Events(), EventError)
	if !errors.Is(event.Err, visibleErr) {
		t.Fatalf("visible EventError = %v, want %v", event.Err, visibleErr)
	}
	if got := c.Snapshot().LastError; got != visibleErr.Error() {
		t.Fatalf("LastError after visible transport error = %q, want %q", got, visibleErr)
	}
}

func TestStatusWatchdogFailsRunningAndPausedPrograms(t *testing.T) {
	for _, paused := range []bool{false, true} {
		name := "running"
		if paused {
			name = "paused"
		}
		t.Run(name, func(t *testing.T) {
			c, fake := connectWatchdogController(t, true)
			path := writeProgramFile(t, "blocked.gcode", "G1 X1\n")
			if err := c.LoadProgramFile(path); err != nil {
				t.Fatalf("LoadProgramFile() error = %v", err)
			}
			if err := c.StartProgram(context.Background()); err != nil {
				t.Fatalf("StartProgram() error = %v", err)
			}
			waitForWrites(t, fake, 1)
			if paused {
				if err := c.PauseProgram(context.Background()); err != nil {
					t.Fatalf("PauseProgram() error = %v", err)
				}
				waitForState(t, c, func(s State) bool { return s.ProgramStatus == ProgramPaused })
			}
			c.mu.RLock()
			session := c.responseOwner.session
			c.mu.RUnlock()

			fake.SetResponding(false)
			state := waitForEStop(t, c, EStopSourceUnresponsive)
			if state.ProgramStatus != ProgramFailed {
				t.Fatalf("ProgramStatus = %q, want %q", state.ProgramStatus, ProgramFailed)
			}
			c.mu.RLock()
			run, owner := c.run, c.responseOwner.kind
			c.mu.RUnlock()
			if run != nil || owner != responseOwnerNone {
				t.Fatalf("active work remains: run=%p owner=%v", run, owner)
			}
			select {
			case <-session.Done():
				if !errors.Is(session.Err(), ErrEmergencyStop) {
					t.Fatalf("program session error = %v, want ErrEmergencyStop", session.Err())
				}
			case <-time.After(time.Second):
				t.Fatal("program response waiter was not released")
			}
		})
	}
}

func TestStatusWatchdogCancelsInteractiveResponseOwner(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	if err := c.writeManualLine(context.Background(), "G1 X1"); err != nil {
		t.Fatalf("writeManualLine() error = %v", err)
	}
	c.mu.RLock()
	session := c.responseOwner.session
	c.mu.RUnlock()
	if session == nil {
		t.Fatal("manual response session was not installed")
	}

	fake.SetResponding(false)
	waitForEStop(t, c, EStopSourceUnresponsive)
	select {
	case <-session.Done():
		if !errors.Is(session.Err(), ErrEmergencyStop) {
			t.Fatalf("session error = %v, want ErrEmergencyStop", session.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("manual response session was not canceled")
	}
	if got := c.Snapshot().ConnectionStatus; got != ConnectionConnected {
		t.Fatalf("ConnectionStatus = %q", got)
	}
}

func TestAlarm50ImmediatelyEntersEStopWithAndWithoutOwner(t *testing.T) {
	for _, withOwner := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "with_owner"}[withOwner], func(t *testing.T) {
			c, fake := connectWatchdogController(t, true)
			if withOwner {
				if err := c.writeManualLine(context.Background(), "G1 X1"); err != nil {
					t.Fatalf("writeManualLine() error = %v", err)
				}
			}
			fake.SetResponding(false)
			started := time.Now()
			fake.InjectRX("ALARM:50")
			state := waitForEStop(t, c, EStopSourceAlarm50)
			if time.Since(started) >= testResponseTimeout {
				t.Fatalf("ALARM:50 waited for watchdog timeout: %v", time.Since(started))
			}
			if state.ConnectionStatus != ConnectionConnected || !fake.IsOpen() {
				t.Fatalf("ALARM:50 disconnected controller: %+v", state)
			}
		})
	}
}

func TestOtherAlarmCodesDoNotEnterEStop(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	for _, line := range []string{"ALARM:1", "ALARM:2", "ALARM:9"} {
		fake.InjectRX(line)
	}
	time.Sleep(30 * time.Millisecond)
	if state := c.Snapshot(); state.EStopStatus != EStopClear {
		t.Fatalf("state after ordinary alarms = %+v", state)
	}
}

func TestAlarm50RecoveryUsesRealtimeStatus(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	fake.SetResponding(false)
	fake.InjectRX("ALARM:50")
	waitForEStop(t, c, EStopSourceAlarm50)

	fake.SetStatusResponse("<Alarm|MPos:0,0,0>")
	fake.SetResponding(true)
	state := waitForState(t, c, func(s State) bool { return s.EStopStatus == EStopRecovery })
	if state.EStopSource != EStopSourceAlarm50 {
		t.Fatalf("recovery source = %q", state.EStopSource)
	}
	fake.SetStatusResponse("<Idle|MPos:0,0,0>")
	state = waitForState(t, c, func(s State) bool { return s.EStopStatus == EStopClear })
	if state.EStopSource != EStopSourceNone {
		t.Fatalf("cleared source = %q", state.EStopSource)
	}
}

func TestManualOperationDoesNotSuppressStatusWatchdog(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	if err := c.Jog(context.Background(), "X", 1, 100); err != nil {
		t.Fatalf("Jog() error = %v", err)
	}
	fake.SetResponding(false)
	state := waitForEStop(t, c, EStopSourceUnresponsive)
	if state.ConnectionStatus != ConnectionConnected {
		t.Fatalf("state = %+v", state)
	}
}

func TestEStopInvalidatesTelemetry(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	c.mu.Lock()
	c.controllerResponseTimeout = 250 * time.Millisecond
	c.mu.Unlock()
	fake.SetResponding(false)
	fake.InjectRX("<Run|MPos:1,2,3|WPos:4,5,6|W:7,8,9|FS:100,200>")
	waitForState(t, c, func(s State) bool {
		return s.HasMachinePosition && s.HasWorkPosition && s.HasWorkCoordinateOffset && s.HasFeedSpindle
	})
	state := waitForEStop(t, c, EStopSourceUnresponsive)
	if state.MachineState != "" || state.HasMachinePosition || state.HasWorkPosition || state.HasWorkCoordinateOffset || state.HasFeedSpindle || state.LastStatusRaw != "" {
		t.Fatalf("stale telemetry retained: %+v", state)
	}
}

func TestEStopRecoveryRequiresFreshUsableStatusAndNeverResumesProgram(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	path := writeProgramFile(t, "blocked.gcode", "G1 X1\n")
	if err := c.LoadProgramFile(path); err != nil {
		t.Fatalf("LoadProgramFile() error = %v", err)
	}
	if err := c.StartProgram(context.Background()); err != nil {
		t.Fatalf("StartProgram() error = %v", err)
	}
	waitForWrites(t, fake, 1)
	fake.SetResponding(false)
	active := waitForEStop(t, c, EStopSourceUnresponsive)
	if active.ProgramStatus != ProgramFailed || !strings.Contains(active.LastError, "emergency stop: controller stopped responding") {
		t.Fatalf("active e-stop did not retain program failure: %+v", active)
	}

	fake.SetStatusResponse("<Alarm|MPos:0,0,0|FS:0,0>")
	fake.SetResponding(true)
	state := waitForState(t, c, func(s State) bool { return s.EStopStatus == EStopRecovery })
	if state.ConnectionStatus != ConnectionConnected || state.ProgramStatus != ProgramFailed || state.EStopSource != EStopSourceUnresponsive {
		t.Fatalf("recovery state = %+v", state)
	}
	if err := c.Action(context.Background(), grbl.ActionSoftReset); err != nil {
		t.Fatalf("Soft Reset in recovery error = %v", err)
	}
	if err := c.Action(context.Background(), grbl.ActionUnlock); err != nil {
		t.Fatalf("Unlock in recovery error = %v", err)
	}

	fake.SetStatusResponse("<Idle|MPos:0,0,0|FS:0,0>")
	state = waitForState(t, c, func(s State) bool { return s.EStopStatus == EStopClear })
	if state.EStopSource != EStopSourceNone || state.ProgramStatus != ProgramFailed || !strings.Contains(state.LastError, "emergency stop: controller stopped responding") {
		t.Fatalf("cleared state = %+v", state)
	}
}

func TestEStopRecoveryAdmission(t *testing.T) {
	c, fake := connectWatchdogController(t, false)
	waitForEStop(t, c, EStopSourceUnresponsive)
	fake.SetStatusResponse("<Alarm|MPos:0,0,0>")
	fake.SetResponding(true)
	waitForState(t, c, func(s State) bool { return s.EStopStatus == EStopRecovery })

	if err := c.Jog(context.Background(), "X", 1, 100); !errors.Is(err, ErrEmergencyStopActive) {
		t.Fatalf("Jog() in recovery error = %v, want ErrEmergencyStopActive", err)
	}
	if err := c.Action(context.Background(), grbl.ActionSoftReset); err != nil {
		t.Fatalf("Soft Reset in recovery error = %v", err)
	}
	if err := c.Action(context.Background(), grbl.ActionUnlock); err != nil {
		t.Fatalf("Unlock in recovery error = %v", err)
	}
}

func TestEStopActiveAdmissionAndExplicitDisconnect(t *testing.T) {
	c, fake := connectWatchdogController(t, false)
	waitForEStop(t, c, EStopSourceUnresponsive)

	checks := []struct {
		name string
		fn   func() error
	}{
		{name: "program", fn: func() error { return c.StartProgram(context.Background()) }},
		{name: "jog", fn: func() error { return c.Jog(context.Background(), "X", 1, 100) }},
		{name: "spindle", fn: func() error { return c.StartSpindleCW(context.Background(), 1000) }},
		{name: "console", fn: func() error { return c.SendConsoleLine(context.Background(), "G1 X1") }},
		{name: "reset", fn: func() error { return c.Action(context.Background(), grbl.ActionSoftReset) }},
		{name: "unlock", fn: func() error { return c.Action(context.Background(), grbl.ActionUnlock) }},
	}
	for _, check := range checks {
		if err := check.fn(); !errors.Is(err, ErrEmergencyStopActive) {
			t.Errorf("%s error = %v, want ErrEmergencyStopActive", check.name, err)
		}
	}
	if err := c.Disconnect(); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	state := c.Snapshot()
	if state.ConnectionStatus != ConnectionDisconnected || state.EStopStatus != EStopClear || state.EStopSource != EStopSourceNone || fake.IsOpen() {
		t.Fatalf("state after disconnect = %+v, open=%v", state, fake.IsOpen())
	}
}

func TestTransportDisconnectWinsEStopRace(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	generation := fake.Generation()
	fake.SetResponding(false)
	go func() {
		time.Sleep(testResponseTimeout)
		if fake.Generation() == generation {
			fake.InjectDisconnected()
		}
	}()
	state := waitForState(t, c, func(s State) bool { return s.ConnectionStatus == ConnectionDisconnected })
	if state.EStopStatus != EStopClear || state.EStopSource != EStopSourceNone {
		t.Fatalf("disconnect left e-stop latched: %+v", state)
	}
}

func TestStaleGenerationWatchdogCannotAffectReconnect(t *testing.T) {
	c, fake := connectWatchdogController(t, true)
	oldGeneration := fake.Generation()
	if err := c.Disconnect(); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	if err := c.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
		t.Fatalf("reconnect error = %v", err)
	}
	waitForState(t, c, func(s State) bool { return s.ConnectionStatus == ConnectionConnected })
	c.checkStatusWatchdog(oldGeneration, time.Now().Add(time.Hour))
	state := c.Snapshot()
	if state.EStopStatus != EStopClear || state.ConnectionStatus != ConnectionConnected || fake.Generation() == oldGeneration {
		t.Fatalf("new generation affected by stale watchdog: %+v", state)
	}
}
