package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ianbruene/ddgo/internal/transport"
)

func connectSettingsMutationController(t *testing.T) (*Controller, *transport.FakeTransport) {
	t.Helper()
	fake := transport.NewFakeTransport()
	c := NewController(fake, nil)
	c.statusPollInterval = time.Hour
	if err := c.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	c.mu.Lock()
	c.setGRBLSettingsLocked(c.connectionGeneration, defaultGG3TimingValues())
	c.mu.Unlock()
	fake.SetResponding(false)
	t.Cleanup(func() {
		fake.SetWriteError(nil)
		_ = fake.Close()
	})
	return c, fake
}

func waitForSettingValue(t *testing.T, c *Controller, number int, want float64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.grblSettingsSnapshotCopy().values[number]; got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("setting $%d = %v, want %v", number, c.grblSettingsSnapshotCopy().values[number], want)
}

func waitForNoResponseOwner(t *testing.T, c *Controller) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		kind := c.responseOwner.kind
		c.mu.RUnlock()
		if kind == responseOwnerNone {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("response owner was not released")
}

func TestManualSettingsMutationCommitsOnlyOnOK(t *testing.T) {
	c, fake := connectSettingsMutationController(t)
	if err := c.SendConsoleLine(context.Background(), "$24=20"); err != nil {
		t.Fatal(err)
	}
	if got := c.grblSettingsSnapshotCopy().values[24]; got != 30 {
		t.Fatalf("setting committed before ok: %v", got)
	}
	fake.InjectRX("ok")
	waitForSettingValue(t, c, 24, 20)

	if err := c.SendConsoleLine(context.Background(), "$24=10"); err != nil {
		t.Fatal(err)
	}
	fake.InjectRX("error:1")
	waitForNoResponseOwner(t, c)
	if got := c.grblSettingsSnapshotCopy().values[24]; got != 20 {
		t.Fatalf("failed setting mutation changed value to %v", got)
	}
}

func TestManualReportUnitsMutationUsesFirmwareIntegerSemantics(t *testing.T) {
	for _, tt := range []struct {
		line string
		want float64
	}{
		{"$13=0", 0}, {"$13=1", 1}, {"$13=0.9", 0}, {"$13=1.9", 1},
	} {
		t.Run(tt.line, func(t *testing.T) {
			c, fake := connectSettingsMutationController(t)
			if err := c.SendConsoleLine(context.Background(), tt.line); err != nil {
				t.Fatal(err)
			}
			fake.InjectRX("ok")
			waitForNoResponseOwner(t, c)
			if got := c.grblSettingsSnapshotCopy().values[13]; got != tt.want {
				t.Fatalf("cached $13 = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReportUnitsMutationFailureRetainsSnapshot(t *testing.T) {
	c, fake := connectSettingsMutationController(t)
	if err := c.SendConsoleLine(context.Background(), "$13=1"); err != nil {
		t.Fatal(err)
	}
	fake.InjectRX("error:1")
	waitForNoResponseOwner(t, c)
	if got := c.grblSettingsSnapshotCopy().values[13]; got != 0 {
		t.Fatalf("failed $13 mutation changed value to %v", got)
	}

	wantErr := errors.New("write failed")
	fake.SetWriteError(wantErr)
	if err := c.SendConsoleLine(context.Background(), "$13=1"); !errors.Is(err, wantErr) {
		t.Fatalf("SendConsoleLine() error = %v, want %v", err, wantErr)
	}
	if got := c.grblSettingsSnapshotCopy().values[13]; got != 0 {
		t.Fatalf("failed $13 write changed value to %v", got)
	}
}

func TestSettingsMutationWriteFailureRetainsSnapshot(t *testing.T) {
	c, fake := connectSettingsMutationController(t)
	wantErr := errors.New("write failed")
	fake.SetWriteError(wantErr)
	err := c.SendConsoleLine(context.Background(), "$25=1000")
	if !errors.Is(err, wantErr) {
		t.Fatalf("SendConsoleLine() error = %v, want %v", err, wantErr)
	}
	if got := c.grblSettingsSnapshotCopy().values[25]; got != 2000 {
		t.Fatalf("write failure changed setting to %v", got)
	}
	c.mu.RLock()
	pending := c.pendingSettingsMutation
	c.mu.RUnlock()
	if pending.id != 0 {
		t.Fatalf("write failure leaked mutation: %+v", pending)
	}
}

func TestPersistentSettingsMutationTokenIsolation(t *testing.T) {
	c := NewController(transport.NewFakeTransport(), nil)
	session := newResponseSession(make(chan string, 1))
	owner := responseOwner{kind: responseOwnerInteractiveMacro, session: session, generation: 1}
	c.mu.Lock()
	c.connectionGeneration = 1
	c.responseOwner = owner
	c.setGRBLSettingsLocked(1, defaultGG3TimingValues())
	idA, err := c.beginSettingsMutationLocked(owner, "$24=20")
	if err != nil {
		c.mu.Unlock()
		t.Fatal(err)
	}
	if !c.commitSettingsMutationLocked(idA) {
		c.mu.Unlock()
		t.Fatal("mutation A did not commit")
	}
	idB, err := c.beginSettingsMutationLocked(owner, "$25=1000")
	if err != nil {
		c.mu.Unlock()
		t.Fatal(err)
	}
	staleCleared := c.clearSettingsMutationLocked(idA)
	pending := c.pendingSettingsMutation
	if !c.commitSettingsMutationLocked(idB) {
		c.mu.Unlock()
		t.Fatal("mutation B did not commit")
	}
	values := cloneGRBLSettings(c.grblSettings.values)
	c.mu.Unlock()
	if staleCleared || pending.id != idB || values[24] != 20 || values[25] != 1000 {
		t.Fatalf("token isolation: staleCleared=%t pending=%+v values=%v", staleCleared, pending, values)
	}
}

func TestInteractiveSettingsMutationUsesExactCompletion(t *testing.T) {
	c, fake := connectSettingsMutationController(t)
	session, err := c.beginInteractiveSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.endInteractiveSession(session, nil) })
	done := make(chan error, 1)
	go func() { done <- c.runtimeForSession(session).sendLineAndWaitOK(context.Background(), "$110=1800", 0) }()
	waitForLineWrites(t, fake, "$110=1800")
	if got := c.grblSettingsSnapshotCopy().values[110]; got != 2540 {
		t.Fatalf("interactive mutation committed before ok: %v", got)
	}
	fake.InjectRX("ok")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := c.grblSettingsSnapshotCopy().values[110]; got != 1800 {
		t.Fatalf("interactive mutation value = %v, want 1800", got)
	}
	c.mu.RLock()
	owner := c.responseOwner
	c.mu.RUnlock()
	if owner.kind != responseOwnerInteractiveMacro {
		t.Fatalf("persistent owner was released: %+v", owner)
	}
}

func TestInteractiveReportUnitsMutationNormalizesFraction(t *testing.T) {
	c, fake := connectSettingsMutationController(t)
	session, err := c.beginInteractiveSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.endInteractiveSession(session, nil) })
	done := make(chan error, 1)
	go func() { done <- c.runtimeForSession(session).sendLineAndWaitOK(context.Background(), "$13=1.9", 0) }()
	waitForLineWrites(t, fake, "$13=1.9")
	fake.InjectRX("ok")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := c.grblSettingsSnapshotCopy().values[13]; got != 1 {
		t.Fatalf("interactive $13 value = %v, want 1", got)
	}
}

func waitForLevelAdjustmentCount(t *testing.T, c *Controller, want int) expectedSilenceLease {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		lease := expectedSilenceSnapshot(c)
		if lease.adjustmentCount == want {
			return lease
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("level adjustment count = %d, want %d", expectedSilenceSnapshot(c).adjustmentCount, want)
	return expectedSilenceLease{}
}

func TestSuccessfulReportUnitsMutationChangesProductionLevelTiming(t *testing.T) {
	c, fake := connectSettingsMutationController(t)
	if err := c.SendConsoleLine(context.Background(), "$13=1"); err != nil {
		t.Fatal(err)
	}
	fake.InjectRX("ok")
	waitForSettingValue(t, c, 13, 1)

	if err := c.SendConsoleLine(context.Background(), "$L"); err != nil {
		t.Fatal(err)
	}
	lease := waitForExpectedSilence(t, c, true)
	wantLimit, ok := gg3LevelNextInactivityLimit(lease.levelPlan, 1, 1)
	if !ok || !lease.levelPlan.reportUnitsKnown || !lease.levelPlan.reportInches {
		t.Fatalf("inch-report lease plan = %+v, calculation ok=%t", lease.levelPlan, ok)
	}
	fake.InjectRX("[1.0000 mm]")
	updated := waitForLevelAdjustmentCount(t, c, 1)
	if updated.id != lease.id || updated.inactivityLimit != wantLimit {
		t.Fatalf("updated lease = %+v, want id=%d limit=%v", updated, lease.id, wantLimit)
	}
	fake.InjectRX("ok")
	waitForExpectedSilence(t, c, false)
}

func TestSettingsResetInvalidatesSnapshotBeforeWrite(t *testing.T) {
	for _, tt := range []struct {
		line        string
		invalidates bool
	}{
		{"$RST=$", true}, {"$RST=*", true}, {"$RST=#", false},
	} {
		t.Run(tt.line, func(t *testing.T) {
			fake := transport.NewFakeTransport()
			observing := &expectedSilenceObservingTransport{FakeTransport: fake}
			c := NewController(observing, nil)
			c.statusPollInterval = time.Hour
			if err := c.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = fake.Close() })
			c.mu.Lock()
			c.setGRBLSettingsLocked(c.connectionGeneration, defaultGG3TimingValues())
			c.mu.Unlock()
			seen := make(chan grblSettingsSnapshot, 1)
			observing.setWriteObserver(func(msg transport.Message) {
				if msg.Display == tt.line {
					seen <- c.grblSettingsSnapshotCopy()
				}
			})
			fake.SetResponding(false)
			if err := c.SendConsoleLine(context.Background(), tt.line); err != nil {
				t.Fatal(err)
			}
			snapshot := <-seen
			if tt.invalidates && (snapshot.generation != 0 || snapshot.values != nil) {
				t.Fatalf("snapshot at reset write = %+v, want invalid", snapshot)
			}
			if !tt.invalidates && snapshot.generation == 0 {
				t.Fatal("coordinate reset invalidated timing settings")
			}
			fake.InjectRX("ok")
		})
	}
}

func TestSettingsResetWriteFailureLeavesSnapshotInvalid(t *testing.T) {
	c, fake := connectSettingsMutationController(t)
	wantErr := errors.New("reset write failed")
	fake.SetWriteError(wantErr)
	if err := c.SendConsoleLine(context.Background(), "$RST=*"); !errors.Is(err, wantErr) {
		t.Fatalf("SendConsoleLine() error = %v, want %v", err, wantErr)
	}
	if snapshot := c.grblSettingsSnapshotCopy(); snapshot.generation != 0 || snapshot.values != nil {
		t.Fatalf("failed reset write retained snapshot: %+v", snapshot)
	}
}

func TestSettingsMutationGenerationSafety(t *testing.T) {
	c := NewController(transport.NewFakeTransport(), nil)
	ownerA := responseOwner{kind: responseOwnerInteractiveMacro, session: newResponseSession(make(chan string, 1)), generation: 1}
	c.mu.Lock()
	c.connectionGeneration = 1
	c.responseOwner = ownerA
	c.setGRBLSettingsLocked(1, defaultGG3TimingValues())
	idA, err := c.beginSettingsMutationLocked(ownerA, "$24=20")
	if err != nil {
		c.mu.Unlock()
		t.Fatal(err)
	}
	c.connectionGeneration = 2
	c.setGRBLSettingsLocked(2, defaultGG3TimingValues())
	committed := c.commitSettingsMutationLocked(idA)
	value := c.grblSettings.values[24]
	c.mu.Unlock()
	if committed || value != 30 {
		t.Fatalf("stale generation committed=%t value=%v", committed, value)
	}
}

func TestConnectionTeardownClearsSettingsMutationAndSnapshot(t *testing.T) {
	c := NewController(transport.NewFakeTransport(), nil)
	owner := responseOwner{kind: responseOwnerInteractiveMacro, session: newResponseSession(make(chan string, 1)), generation: 1}
	c.mu.Lock()
	c.connectionGeneration = 1
	c.state.ConnectionStatus = ConnectionConnected
	c.responseOwner = owner
	c.setGRBLSettingsLocked(1, defaultGG3TimingValues())
	if _, err := c.beginSettingsMutationLocked(owner, "$24=20"); err != nil {
		c.mu.Unlock()
		t.Fatal(err)
	}
	c.clearConnectionStateLocked()
	pending := c.pendingSettingsMutation
	snapshot := c.grblSettingsSnapshotLocked()
	c.mu.Unlock()
	if pending.id != 0 || snapshot.generation != 0 || snapshot.values != nil {
		t.Fatalf("teardown left mutation/settings: pending=%+v snapshot=%+v", pending, snapshot)
	}
}

func TestResetInvalidationDisablesProductionExpectedSilence(t *testing.T) {
	c, fake := connectSettingsMutationController(t)
	if err := c.SendConsoleLine(context.Background(), "$RST=$"); err != nil {
		t.Fatal(err)
	}
	fake.InjectRX("ok")
	waitForNoResponseOwner(t, c)
	if err := c.SendConsoleLine(context.Background(), "$H"); err != nil {
		t.Fatal(err)
	}
	if lease := expectedSilenceSnapshot(c); lease.id != 0 {
		t.Fatalf("invalidated settings allowed production lease: %+v", lease)
	}
	fake.InjectRX("ok")
}
