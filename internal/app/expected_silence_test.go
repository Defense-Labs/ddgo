package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ianbruene/ddgo/internal/transport"
)

type expectedSilenceObservingTransport struct {
	*transport.FakeTransport
	mu      sync.Mutex
	onWrite func(transport.Message)
}

func (t *expectedSilenceObservingTransport) setWriteObserver(observer func(transport.Message)) {
	t.mu.Lock()
	t.onWrite = observer
	t.mu.Unlock()
}

func (t *expectedSilenceObservingTransport) Write(ctx context.Context, msg transport.Message) error {
	t.mu.Lock()
	observer := t.onWrite
	t.mu.Unlock()
	if observer != nil {
		observer(msg)
	}
	return t.FakeTransport.Write(ctx, msg)
}

func expectedSilenceTestPolicy(limit time.Duration, classes map[string]expectedSilenceClass) expectedSilencePolicy {
	return func(line string) (expectedSilenceClass, time.Duration, bool) {
		class, ok := classes[line]
		return class, limit, ok
	}
}

func connectExpectedSilenceController(t *testing.T, limit time.Duration, classes map[string]expectedSilenceClass) (*Controller, *transport.FakeTransport, *expectedSilenceObservingTransport) {
	t.Helper()
	fake := transport.NewFakeTransport()
	observing := &expectedSilenceObservingTransport{FakeTransport: fake}
	c := NewController(observing, nil)
	c.statusPollInterval = 10 * time.Millisecond
	c.controllerResponseTimeout = 500 * time.Millisecond
	c.connectionStartupQuietPeriod = 5 * time.Millisecond
	c.expectedSilencePolicy = expectedSilenceTestPolicy(limit, classes)
	if err := c.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	waitForState(t, c, func(state State) bool {
		return state.ConnectionStatus == ConnectionConnected && state.MachineState == "Idle"
	})
	t.Cleanup(func() {
		fake.SetWriteError(nil)
		_ = fake.Close()
	})
	return c, fake, observing
}

func expectedSilenceSnapshot(c *Controller) expectedSilenceLease {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.expectedSilence
}

func waitForExpectedSilence(t *testing.T, c *Controller, active bool) expectedSilenceLease {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		lease := expectedSilenceSnapshot(c)
		if (lease.id != 0) == active {
			return lease
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for expected-silence active=%t; lease=%+v", active, expectedSilenceSnapshot(c))
	return expectedSilenceLease{}
}

func TestExpectedSilenceManualLeaseSuppressesAndResumesPolling(t *testing.T) {
	c, fake, observing := connectExpectedSilenceController(t, 0, map[string]expectedSilenceClass{
		"$H": expectedSilenceHomeAll,
	})
	fake.SetResponding(false)

	c.mu.Lock()
	c.statusWatchdogStartedAt = time.Now()
	c.pendingQuietStatusReports = 3
	c.mu.Unlock()

	leaseSeenAtWrite := make(chan expectedSilenceLease, 1)
	observing.setWriteObserver(func(msg transport.Message) {
		if msg.Display != "$H" {
			return
		}
		c.mu.RLock()
		lease := c.expectedSilence
		heartbeat := c.statusWatchdogStartedAt
		pending := c.pendingQuietStatusReports
		c.mu.RUnlock()
		if lease.id == 0 || !heartbeat.IsZero() || pending != 0 {
			leaseSeenAtWrite <- expectedSilenceLease{}
			return
		}
		leaseSeenAtWrite <- lease
	})

	if err := c.Home(context.Background()); err != nil {
		t.Fatalf("Home() error = %v", err)
	}
	lease := <-leaseSeenAtWrite
	if lease.id == 0 || lease.class != expectedSilenceHomeAll {
		t.Fatalf("lease at command write = %+v", lease)
	}

	statusWrites := countStatusWrites(fake.Written())
	time.Sleep(5 * c.statusPollInterval)
	if got := countStatusWrites(fake.Written()); got != statusWrites {
		t.Fatalf("automatic status writes during lease = %d, want %d", got, statusWrites)
	}
	c.mu.RLock()
	pollerAlive := c.statusPollCancel != nil && c.statusPollDone != nil
	c.mu.RUnlock()
	if !pollerAlive {
		t.Fatal("status polling goroutine was stopped during expected silence")
	}

	fake.InjectRX("controller output")
	waitForEventText(t, c.Events(), EventConsoleRX, "controller output")
	if got := expectedSilenceSnapshot(c).id; got != lease.id {
		t.Fatalf("arbitrary RX ended lease: got %d, want %d", got, lease.id)
	}
	fake.InjectRX("<Run|MPos:1,2,3>")
	waitForState(t, c, func(state State) bool { return state.MachineState == "Run" })
	if got := expectedSilenceSnapshot(c).id; got != lease.id {
		t.Fatalf("status report ended lease: got %d, want %d", got, lease.id)
	}
	fake.InjectRX("<Idle|MPos:1,2,3>")
	waitForState(t, c, func(state State) bool { return state.MachineState == "Idle" })
	if got := expectedSilenceSnapshot(c).id; got != lease.id {
		t.Fatalf("Idle status ended lease: got %d, want %d", got, lease.id)
	}
	fake.InjectRX("[MSG:Reset to cont]")
	waitForEventText(t, c.Events(), EventConsoleRX, "[MSG:Reset to cont]")
	if got := expectedSilenceSnapshot(c).id; got != lease.id {
		t.Fatalf("MSG line ended lease: got %d, want %d", got, lease.id)
	}

	fake.InjectRX("ok")
	waitForExpectedSilence(t, c, false)
	waitForStatusWritesAfter(t, fake, statusWrites)
}

func TestExpectedSilenceTerminalResponsesEndManualLease(t *testing.T) {
	for _, terminal := range []string{"ok", "error:1", "ALARM:1"} {
		t.Run(terminal, func(t *testing.T) {
			c, fake, _ := connectExpectedSilenceController(t, 0, map[string]expectedSilenceClass{
				"$H": expectedSilenceHomeAll,
			})
			fake.SetResponding(false)
			if err := c.Home(context.Background()); err != nil {
				t.Fatalf("Home() error = %v", err)
			}
			waitForExpectedSilence(t, c, true)
			statusWrites := countStatusWrites(fake.Written())
			fake.InjectRX(terminal)
			waitForExpectedSilence(t, c, false)
			waitForStatusWritesAfter(t, fake, statusWrites)
			c.mu.RLock()
			owner := c.responseOwner
			c.mu.RUnlock()
			if owner.kind != responseOwnerNone {
				t.Fatalf("response owner after %q = %+v", terminal, owner)
			}
		})
	}
}

func TestExpectedSilenceActivityAndExpiration(t *testing.T) {
	c := NewController(transport.NewFakeTransport(), nil)
	const generation transport.ConnectionGeneration = 7
	startedAt := time.Unix(1000, 0)
	owner := responseOwner{kind: responseOwnerManualLine, session: newResponseSession(make(chan string, 1)), generation: generation}

	c.now = func() time.Time { return startedAt }
	c.mu.Lock()
	c.connectionGeneration = generation
	c.statusMonitoringGeneration = generation
	c.state.ConnectionStatus = ConnectionConnected
	c.responseOwner = owner
	id, err := c.beginExpectedSilenceLocked(owner, expectedSilenceSpec{class: expectedSilenceHomeAll, inactivityLimit: 100 * time.Millisecond})
	if err != nil {
		c.mu.Unlock()
		t.Fatalf("beginExpectedSilenceLocked() error = %v", err)
	}
	c.noteControllerActivityLocked(generation, startedAt.Add(-time.Millisecond))
	c.noteControllerActivityLocked(generation+1, startedAt.Add(50*time.Millisecond))
	if !c.expectedSilence.lastActivity.IsZero() {
		c.mu.Unlock()
		t.Fatalf("old/stale RX refreshed lease: %+v", c.expectedSilence)
	}
	activityAt := startedAt.Add(80 * time.Millisecond)
	c.noteControllerActivityLocked(generation, activityAt)
	c.noteControllerActivityLocked(generation, startedAt.Add(40*time.Millisecond))
	lease := c.expectedSilence
	c.mu.Unlock()

	if lease.id != id || lease.lastActivity != activityAt {
		t.Fatalf("lease activity = %+v, want id=%d activity=%v", lease, id, activityAt)
	}
	c.checkStatusWatchdog(generation, startedAt.Add(150*time.Millisecond))
	if state := c.Snapshot(); state.EStopStatus != EStopClear {
		t.Fatalf("activity did not extend silence timeout: %+v", state)
	}
	c.checkStatusWatchdog(generation, startedAt.Add(181*time.Millisecond))
	state := c.Snapshot()
	if state.EStopStatus != EStopActive || state.EStopSource != EStopSourceUnresponsive {
		t.Fatalf("silence expiration state = %+v", state)
	}
	if lease := expectedSilenceSnapshot(c); lease.id != 0 {
		t.Fatalf("expired lease remains active: %+v", lease)
	}
	if !errors.Is(owner.session.Err(), ErrEmergencyStop) {
		t.Fatalf("owner session error = %v, want ErrEmergencyStop", owner.session.Err())
	}
}

func TestExpectedSilenceZeroLimitDoesNotExpire(t *testing.T) {
	c := NewController(transport.NewFakeTransport(), nil)
	const generation transport.ConnectionGeneration = 8
	startedAt := time.Unix(1100, 0)
	owner := responseOwner{kind: responseOwnerManualLine, session: newResponseSession(make(chan string, 1)), generation: generation}
	c.now = func() time.Time { return startedAt }
	c.mu.Lock()
	c.connectionGeneration = generation
	c.statusMonitoringGeneration = generation
	c.state.ConnectionStatus = ConnectionConnected
	c.responseOwner = owner
	if _, err := c.beginExpectedSilenceLocked(owner, expectedSilenceSpec{class: expectedSilenceLevel}); err != nil {
		c.mu.Unlock()
		t.Fatalf("beginExpectedSilenceLocked() error = %v", err)
	}
	c.mu.Unlock()
	c.checkStatusWatchdog(generation, startedAt.Add(24*time.Hour))
	if state := c.Snapshot(); state.EStopStatus != EStopClear {
		t.Fatalf("zero-limit lease expired: %+v", state)
	}
}

func TestExpectedSilenceWatchdogUsesLeaseInactivity(t *testing.T) {
	c, fake, _ := connectExpectedSilenceController(t, 80*time.Millisecond, map[string]expectedSilenceClass{
		"$H": expectedSilenceHomeAll,
	})
	fake.SetResponding(false)
	if err := c.Home(context.Background()); err != nil {
		t.Fatalf("Home() error = %v", err)
	}
	lease := waitForExpectedSilence(t, c, true)

	for i := 0; i < 6; i++ {
		time.Sleep(30 * time.Millisecond)
		fake.InjectRX("activity")
		waitForEventText(t, c.Events(), EventConsoleRX, "activity")
		if state := c.Snapshot(); state.EStopStatus != EStopClear {
			t.Fatalf("periodic RX did not prevent silence timeout: %+v", state)
		}
	}
	refreshed := expectedSilenceSnapshot(c)
	if refreshed.id != lease.id || refreshed.lastActivity.IsZero() || refreshed.lastActivity.Before(refreshed.startedAt) {
		t.Fatalf("lease activity was not refreshed: before=%+v after=%+v", lease, refreshed)
	}

	state := waitForEStop(t, c, EStopSourceUnresponsive)
	if state.ConnectionStatus != ConnectionConnected {
		t.Fatalf("silence timeout disconnected controller: %+v", state)
	}
	if active := expectedSilenceSnapshot(c); active.id != 0 {
		t.Fatalf("silence timeout left lease active: %+v", active)
	}
	if !errors.Is(lease.owner.session.Err(), ErrEmergencyStop) {
		t.Fatalf("owner session error = %v, want ErrEmergencyStop", lease.owner.session.Err())
	}
}

func TestExpectedSilenceAlarm50IsImmediate(t *testing.T) {
	c, fake, _ := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
		"$H": expectedSilenceHomeAll,
	})
	fake.SetResponding(false)
	if err := c.Home(context.Background()); err != nil {
		t.Fatalf("Home() error = %v", err)
	}
	waitForExpectedSilence(t, c, true)
	fake.InjectRX("ALARM:50")
	state := waitForEStop(t, c, EStopSourceAlarm50)
	if state.ConnectionStatus != ConnectionConnected {
		t.Fatalf("ALARM:50 disconnected controller: %+v", state)
	}
	if lease := expectedSilenceSnapshot(c); lease.id != 0 {
		t.Fatalf("ALARM:50 left lease active: %+v", lease)
	}
}

func TestExpectedSilenceManualWriteFailureCleansUp(t *testing.T) {
	c, fake, _ := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
		"$H": expectedSilenceHomeAll,
	})
	wantErr := errors.New("silent write failed")
	fake.SetWriteError(wantErr)
	err := c.Home(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Home() error = %v, want %v", err, wantErr)
	}
	if lease := expectedSilenceSnapshot(c); lease.id != 0 {
		t.Fatalf("write failure leaked lease: %+v", lease)
	}
	c.mu.RLock()
	owner := c.responseOwner
	c.mu.RUnlock()
	if owner.kind != responseOwnerNone {
		t.Fatalf("write failure leaked response owner: %+v", owner)
	}
	fake.SetWriteError(nil)
	waitForStatusWritesAfter(t, fake, countStatusWrites(fake.Written()))
}

func TestExpectedSilenceInteractiveCommandAndQueryLifecycle(t *testing.T) {
	t.Run("terminal command", func(t *testing.T) {
		c, fake, _ := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
			"$H": expectedSilenceHomeAll,
		})
		fake.SetResponding(false)
		session, err := c.beginInteractiveSession(context.Background())
		if err != nil {
			t.Fatalf("beginInteractiveSession() error = %v", err)
		}
		done := make(chan error, 1)
		go func() {
			done <- c.runtimeForSession(session).sendLineAndWaitOK(context.Background(), "$H", 0)
		}()
		waitForExpectedSilence(t, c, true)
		fake.InjectRX("ok")
		if err := <-done; err != nil {
			t.Fatalf("sendLineAndWaitOK() error = %v", err)
		}
		if lease := expectedSilenceSnapshot(c); lease.id != 0 {
			t.Fatalf("terminal response left lease active: %+v", lease)
		}
		c.mu.RLock()
		owner := c.responseOwner
		c.mu.RUnlock()
		if owner.kind != responseOwnerInteractiveMacro || owner.session != session {
			t.Fatalf("terminal command released persistent owner: %+v", owner)
		}
		c.endInteractiveSession(session, nil)
	})

	t.Run("collecting responses", func(t *testing.T) {
		c, fake, _ := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
			"$#": expectedSilenceLevelSave,
		})
		fake.SetResponding(false)
		session, err := c.beginInteractiveSession(context.Background())
		if err != nil {
			t.Fatalf("beginInteractiveSession() error = %v", err)
		}
		type result struct {
			lines []string
			err   error
		}
		done := make(chan result, 1)
		go func() {
			lines, err := c.runtimeForSession(session).sendLineCollectingResponses(context.Background(), "$#")
			done <- result{lines: lines, err: err}
		}()
		lease := waitForExpectedSilence(t, c, true)
		fake.InjectRX("[G54:1.000,2.000,3.000]")
		waitForEventText(t, c.Events(), EventConsoleRX, "[G54:1.000,2.000,3.000]")
		if got := expectedSilenceSnapshot(c).id; got != lease.id {
			t.Fatalf("query payload ended lease: got %d, want %d", got, lease.id)
		}
		fake.InjectRX("ok")
		got := <-done
		if got.err != nil || len(got.lines) != 1 || got.lines[0] != "[G54:1.000,2.000,3.000]" {
			t.Fatalf("query result = %#v, %v", got.lines, got.err)
		}
		if lease := expectedSilenceSnapshot(c); lease.id != 0 {
			t.Fatalf("query terminal response left lease active: %+v", lease)
		}
		c.endInteractiveSession(session, nil)
	})
}

func TestExpectedSilenceInteractiveTerminalFailures(t *testing.T) {
	for _, terminal := range []string{"error:1", "ALARM:1"} {
		t.Run(terminal, func(t *testing.T) {
			c, fake, _ := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
				"$H": expectedSilenceHomeAll,
			})
			fake.SetResponding(false)
			session, err := c.beginInteractiveSession(context.Background())
			if err != nil {
				t.Fatalf("beginInteractiveSession() error = %v", err)
			}
			done := make(chan error, 1)
			go func() {
				done <- c.runtimeForSession(session).sendLineAndWaitOK(context.Background(), "$H", 0)
			}()
			waitForExpectedSilence(t, c, true)
			fake.InjectRX(terminal)
			if err := <-done; err == nil || !strings.Contains(err.Error(), terminal) {
				t.Fatalf("sendLineAndWaitOK() error = %v after %q", err, terminal)
			}
			if lease := expectedSilenceSnapshot(c); lease.id != 0 {
				t.Fatalf("terminal failure left lease active: %+v", lease)
			}
			c.endInteractiveSession(session, nil)
		})
	}
}

func TestExpectedSilenceCollectingTerminalFailures(t *testing.T) {
	for _, terminal := range []string{"error:1", "ALARM:1"} {
		t.Run(terminal, func(t *testing.T) {
			c, fake, _ := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
				"$#": expectedSilenceLevelSave,
			})
			fake.SetResponding(false)
			session, err := c.beginInteractiveSession(context.Background())
			if err != nil {
				t.Fatalf("beginInteractiveSession() error = %v", err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := c.runtimeForSession(session).sendLineCollectingResponses(context.Background(), "$#")
				done <- err
			}()
			waitForExpectedSilence(t, c, true)
			fake.InjectRX(terminal)
			if err := <-done; err == nil || !strings.Contains(err.Error(), terminal) {
				t.Fatalf("sendLineCollectingResponses() error = %v after %q", err, terminal)
			}
			if lease := expectedSilenceSnapshot(c); lease.id != 0 {
				t.Fatalf("query terminal failure left lease active: %+v", lease)
			}
			c.endInteractiveSession(session, nil)
		})
	}
}

func TestDeliverResponseDoesNotClearPersistentOwnerLease(t *testing.T) {
	c := NewController(transport.NewFakeTransport(), nil)
	const generation transport.ConnectionGeneration = 16
	session := newResponseSession(make(chan string, 1))
	owner := responseOwner{kind: responseOwnerInteractiveMacro, session: session, generation: generation}

	c.mu.Lock()
	c.connectionGeneration = generation
	c.statusMonitoringGeneration = generation
	c.state.ConnectionStatus = ConnectionConnected
	c.responseOwner = owner
	id, err := c.beginExpectedSilenceLocked(owner, expectedSilenceSpec{class: expectedSilenceHomeAll})
	if err != nil {
		c.mu.Unlock()
		t.Fatalf("beginExpectedSilenceLocked() error = %v", err)
	}
	c.deliverResponseLocked(generation, "ok")
	lease := c.expectedSilence
	c.mu.Unlock()

	if lease.id != id {
		t.Fatalf("deliverResponseLocked() cleared lease %d; active lease = %+v", id, lease)
	}
	select {
	case got := <-session.rxCh:
		if got != "ok" {
			t.Fatalf("routed response = %q, want ok", got)
		}
	default:
		t.Fatal("deliverResponseLocked() did not route terminal response")
	}
	c.endExpectedSilence(id)
}

func TestExpectedSilencePersistentOwnerRuntimeTokenIsolation(t *testing.T) {
	c, fake, _ := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
		"$HX": expectedSilenceHomeX,
		"$HY": expectedSilenceHomeY,
	})
	fake.SetResponding(false)
	session, err := c.beginInteractiveSession(context.Background())
	if err != nil {
		t.Fatalf("beginInteractiveSession() error = %v", err)
	}
	runtime := c.runtimeForSession(session)

	commandA := make(chan error, 1)
	go func() { commandA <- runtime.sendLineAndWaitOK(context.Background(), "$HX", 0) }()
	leaseA := waitForExpectedSilence(t, c, true)
	fake.InjectRX("ok")
	if err := <-commandA; err != nil {
		t.Fatalf("command A error = %v", err)
	}
	if lease := expectedSilenceSnapshot(c); lease.id != 0 {
		t.Fatalf("command A terminal response left lease active: %+v", lease)
	}

	commandB := make(chan error, 1)
	go func() { commandB <- runtime.sendLineAndWaitOK(context.Background(), "$HY", 0) }()
	leaseB := waitForExpectedSilence(t, c, true)
	if leaseA.id == leaseB.id {
		t.Fatalf("persistent owner reused lease ID %d", leaseA.id)
	}

	// This represents deferred or otherwise stale cleanup retained by command A.
	// Exact-token cleanup must not affect command B despite the shared owner.
	c.endExpectedSilence(leaseA.id)
	if lease := expectedSilenceSnapshot(c); lease.id != leaseB.id || lease.class != expectedSilenceHomeY {
		t.Fatalf("stale command A cleanup affected lease B: %+v", lease)
	}

	fake.InjectRX("ok")
	if err := <-commandB; err != nil {
		t.Fatalf("command B error = %v", err)
	}
	if lease := expectedSilenceSnapshot(c); lease.id != 0 {
		t.Fatalf("command B terminal response left lease active: %+v", lease)
	}
	c.endInteractiveSession(session, nil)
}

func TestExpectedSilenceOperationTokenIsolation(t *testing.T) {
	c := NewController(transport.NewFakeTransport(), nil)
	const generation transport.ConnectionGeneration = 17
	owner := responseOwner{kind: responseOwnerInteractiveMacro, session: newResponseSession(make(chan string, 1)), generation: generation}
	c.mu.Lock()
	c.connectionGeneration = generation
	c.statusMonitoringGeneration = generation
	c.state.ConnectionStatus = ConnectionConnected
	c.responseOwner = owner
	idA, err := c.beginExpectedSilenceLocked(owner, expectedSilenceSpec{class: expectedSilenceHomeX})
	if err != nil {
		c.mu.Unlock()
		t.Fatalf("begin lease A: %v", err)
	}
	if !c.endExpectedSilenceLocked(idA) {
		c.mu.Unlock()
		t.Fatal("end lease A returned false")
	}
	idB, err := c.beginExpectedSilenceLocked(owner, expectedSilenceSpec{class: expectedSilenceHomeY})
	if err != nil {
		c.mu.Unlock()
		t.Fatalf("begin lease B: %v", err)
	}
	staleEnded := c.endExpectedSilenceLocked(idA)
	lease := c.expectedSilence
	c.mu.Unlock()
	if idA == idB || staleEnded || lease.id != idB || lease.class != expectedSilenceHomeY {
		t.Fatalf("token isolation: idA=%d idB=%d staleEnded=%t lease=%+v", idA, idB, staleEnded, lease)
	}
}

func TestExpectedSilenceSoftResetClearsLease(t *testing.T) {
	c, fake, _ := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
		"$H": expectedSilenceHomeAll,
	})
	fake.SetResponding(false)
	if err := c.Home(context.Background()); err != nil {
		t.Fatalf("Home() error = %v", err)
	}
	waitForExpectedSilence(t, c, true)
	if err := c.SoftReset(context.Background()); err != nil {
		t.Fatalf("SoftReset() error = %v", err)
	}
	if lease := expectedSilenceSnapshot(c); lease.id != 0 {
		t.Fatalf("soft reset left lease active: %+v", lease)
	}
	c.mu.RLock()
	owner := c.responseOwner
	c.mu.RUnlock()
	if owner.kind != responseOwnerNone {
		t.Fatalf("soft reset left owner active: %+v", owner)
	}
}

func TestExpectedSilenceDisconnectCleanup(t *testing.T) {
	for _, unexpected := range []bool{false, true} {
		name := "explicit"
		if unexpected {
			name = "unexpected"
		}
		t.Run(name, func(t *testing.T) {
			c, fake, _ := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
				"$H": expectedSilenceHomeAll,
			})
			fake.SetResponding(false)
			if err := c.Home(context.Background()); err != nil {
				t.Fatalf("Home() error = %v", err)
			}
			waitForExpectedSilence(t, c, true)
			if unexpected {
				fake.InjectDisconnected()
				waitForState(t, c, func(state State) bool { return state.ConnectionStatus == ConnectionDisconnected })
			} else if err := c.Disconnect(); err != nil {
				t.Fatalf("Disconnect() error = %v", err)
			}
			if lease := expectedSilenceSnapshot(c); lease.id != 0 {
				t.Fatalf("disconnect left lease active: %+v", lease)
			}
		})
	}
}

func TestExpectedSilenceSessionCancellationClearsLease(t *testing.T) {
	c, fake, _ := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
		"$H": expectedSilenceHomeAll,
	})
	fake.SetResponding(false)
	session, err := c.beginInteractiveSession(context.Background())
	if err != nil {
		t.Fatalf("beginInteractiveSession() error = %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- c.runtimeForSession(session).sendLineAndWaitOK(context.Background(), "$H", 0)
	}()
	waitForExpectedSilence(t, c, true)
	c.endInteractiveSession(session, context.Canceled)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled command error = %v, want context.Canceled", err)
	}
	if lease := expectedSilenceSnapshot(c); lease.id != 0 {
		t.Fatalf("session cancellation left lease active: %+v", lease)
	}
}

func TestExpectedSilenceProgramCompletionAndCancellation(t *testing.T) {
	t.Run("completion", func(t *testing.T) {
		c, fake, observing := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
			"G1 X1": expectedSilenceLevel,
		})
		fake.SetResponding(false)
		leaseAtWrite := make(chan expectedSilenceID, 1)
		observing.setWriteObserver(func(msg transport.Message) {
			if msg.Display == "G1 X1" {
				leaseAtWrite <- expectedSilenceSnapshot(c).id
			}
		})
		if err := c.LoadProgramFile(writeProgramFile(t, "silent.gcode", "G1 X1\n")); err != nil {
			t.Fatalf("LoadProgramFile() error = %v", err)
		}
		if err := c.StartProgram(context.Background()); err != nil {
			t.Fatalf("StartProgram() error = %v", err)
		}
		lease := waitForExpectedSilence(t, c, true)
		if got := <-leaseAtWrite; got != lease.id {
			t.Fatalf("program lease at transport write = %d, want %d", got, lease.id)
		}
		fake.InjectRX("ok")
		waitForState(t, c, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
		if lease := expectedSilenceSnapshot(c); lease.id != 0 {
			t.Fatalf("program completion left lease active: %+v", lease)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		c, fake, _ := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
			"G1 X1": expectedSilenceLevel,
		})
		fake.SetResponding(false)
		if err := c.LoadProgramFile(writeProgramFile(t, "silent.gcode", "G1 X1\n")); err != nil {
			t.Fatalf("LoadProgramFile() error = %v", err)
		}
		if err := c.StartProgram(context.Background()); err != nil {
			t.Fatalf("StartProgram() error = %v", err)
		}
		waitForExpectedSilence(t, c, true)
		if err := c.StopProgram(context.Background()); err != nil {
			t.Fatalf("StopProgram() error = %v", err)
		}
		if lease := expectedSilenceSnapshot(c); lease.id != 0 {
			t.Fatalf("program cancellation left lease active: %+v", lease)
		}
	})
}

func TestExpectedSilenceSystemTerminalEndsLeaseBeforeFreshIdleBarrier(t *testing.T) {
	fake := transport.NewFakeTransport()
	observing := &expectedSilenceObservingTransport{FakeTransport: fake}
	c := NewController(observing, nil)
	c.statusPollInterval = time.Hour
	c.controllerResponseTimeout = 5 * time.Second
	c.connectionStartupQuietPeriod = 5 * time.Millisecond
	c.expectedSilencePolicy = expectedSilenceTestPolicy(time.Hour, map[string]expectedSilenceClass{
		"$H": expectedSilenceHomeAll,
	})
	if err := c.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = fake.Close() })
	fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForState(t, c, func(state State) bool {
		return state.ConnectionStatus == ConnectionConnected && state.MachineState == "Idle"
	})

	var observerMu sync.Mutex
	commandWritten := false
	postCommandPollLease := make(chan expectedSilenceLease, 1)
	observing.setWriteObserver(func(msg transport.Message) {
		observerMu.Lock()
		afterCommand := commandWritten
		if msg.Display == "$H" {
			commandWritten = true
		}
		observerMu.Unlock()
		if msg.Display == "$H" {
			fake.SetResponding(false)
		}
		if msg.Display == "?" && afterCommand {
			select {
			case postCommandPollLease <- expectedSilenceSnapshot(c):
			default:
			}
		}
	})

	if err := c.LoadProgramFile(writeProgramFile(t, "silent-system.gcode", "$H\n")); err != nil {
		t.Fatalf("LoadProgramFile() error = %v", err)
	}
	if err := c.StartProgram(context.Background()); err != nil {
		t.Fatalf("StartProgram() error = %v", err)
	}
	waitForLineWrites(t, fake, "$H")
	waitForExpectedSilence(t, c, true)
	fake.InjectRX("ok")

	select {
	case lease := <-postCommandPollLease:
		if lease.id != 0 {
			t.Fatalf("post-command Idle poll was written with active lease: %+v", lease)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("post-command fresh Idle status poll was not written")
	}
	if lease := expectedSilenceSnapshot(c); lease.id != 0 {
		t.Fatalf("terminal ok left lease active during Idle barrier: %+v", lease)
	}
	fake.InjectRX("<Idle|MPos:0,0,0>")
	waitForState(t, c, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
}

func TestExpectedSilenceGenerationIsolation(t *testing.T) {
	c, fake, _ := connectExpectedSilenceController(t, time.Hour, map[string]expectedSilenceClass{
		"$H": expectedSilenceHomeAll,
	})
	fake.SetResponding(false)
	if err := c.Home(context.Background()); err != nil {
		t.Fatalf("generation A Home() error = %v", err)
	}
	leaseA := waitForExpectedSilence(t, c, true)
	generationA := fake.Generation()
	if err := c.Disconnect(); err != nil {
		t.Fatalf("generation A Disconnect() error = %v", err)
	}

	fake.SetResponding(true)
	if err := c.Connect(context.Background(), transport.DefaultPortConfig("fake-B")); err != nil {
		t.Fatalf("generation B Connect() error = %v", err)
	}
	waitForState(t, c, func(state State) bool {
		return state.ConnectionStatus == ConnectionConnected && state.MachineState == "Idle"
	})
	generationB := fake.Generation()
	if generationA == generationB {
		t.Fatalf("connection generation did not change: %d", generationA)
	}
	fake.SetResponding(false)
	if err := c.Home(context.Background()); err != nil {
		t.Fatalf("generation B Home() error = %v", err)
	}
	leaseB := waitForExpectedSilence(t, c, true)

	c.mu.Lock()
	staleEnded := c.endExpectedSilenceLocked(leaseA.id)
	c.noteControllerActivityLocked(generationA, leaseB.startedAt.Add(time.Second))
	afterStale := c.expectedSilence
	c.mu.Unlock()
	if staleEnded || afterStale.id != leaseB.id || !afterStale.lastActivity.IsZero() {
		t.Fatalf("stale generation affected lease B: staleEnded=%t lease=%+v", staleEnded, afterStale)
	}

	fake.InjectRXForGeneration(generationA, "ok")
	fake.InjectRXForGeneration(generationB, "<Run|MPos:1,2,3>")
	waitForState(t, c, func(state State) bool { return state.MachineState == "Run" })
	if got := expectedSilenceSnapshot(c).id; got != leaseB.id {
		t.Fatalf("stale RX ended lease B: got %d, want %d", got, leaseB.id)
	}
	fake.InjectRXForGeneration(generationB, "ok")
	waitForExpectedSilence(t, c, false)
}
