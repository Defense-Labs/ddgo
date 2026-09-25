package app

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/ianbruene/ddgo/internal/grbl"
	"github.com/ianbruene/ddgo/internal/transport"
)

func newAdmissionPriorityController(t *testing.T) (*Controller, *transport.FakeTransport) {
	t.Helper()
	fake := transport.NewFakeTransport()
	c := NewController(fake, nil)
	c.statusPollInterval = time.Hour
	if err := c.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	drainEvents(c.Events())
	return c, fake
}

func holdRealtimeAdmission(t *testing.T, c *Controller, request admissionKind) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.beginRealtimeWriteForAdmissionLocked(request); err != nil {
		t.Fatalf("beginRealtimeWriteForAdmissionLocked(%v) error = %v", request, err)
	}
}

func waitForForegroundAdmissionWaiters(t *testing.T, c *Controller, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		got := c.foregroundAdmissionWaiters
		c.mu.RUnlock()
		if got == want {
			return
		}
		runtime.Gosched()
	}
	c.mu.RLock()
	got := c.foregroundAdmissionWaiters
	c.mu.RUnlock()
	t.Fatalf("foregroundAdmissionWaiters = %d, want %d", got, want)
}

func assertOperationWaiting(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("foreground operation returned while status poll held admission: %v", err)
	default:
	}
}

func TestForegroundManualCommandWaitsForStatusPoll(t *testing.T) {
	c, fake := newAdmissionPriorityController(t)
	holdRealtimeAdmission(t, c, admissionStatusPoll)

	done := make(chan error, 1)
	go func() { done <- c.writeManualLine(context.Background(), "G0 X1") }()
	waitForForegroundAdmissionWaiters(t, c, 1)
	assertOperationWaiting(t, done)

	c.endRealtimeWrite()
	if err := waitForErrorResult(t, done); err != nil {
		t.Fatalf("writeManualLine() error = %v", err)
	}
	waitForForegroundAdmissionWaiters(t, c, 0)
	writes := nonStatusWrites(fake)
	if len(writes) != 1 || writes[0].Display != "G0 X1" {
		t.Fatalf("foreground writes = %#v, want G0 X1", writes)
	}
}

func TestForegroundRealtimeActionWaitsForStatusPoll(t *testing.T) {
	c, fake := newAdmissionPriorityController(t)
	holdRealtimeAdmission(t, c, admissionStatusPoll)

	done := make(chan error, 1)
	go func() { done <- c.Action(context.Background(), grbl.ActionHold) }()
	waitForForegroundAdmissionWaiters(t, c, 1)
	assertOperationWaiting(t, done)

	c.endRealtimeWrite()
	if err := waitForErrorResult(t, done); err != nil {
		t.Fatalf("Action(Hold) error = %v", err)
	}
	waitForForegroundAdmissionWaiters(t, c, 0)
	writes := nonStatusWrites(fake)
	if len(writes) != 1 || writes[0].Display != "!" {
		t.Fatalf("foreground writes = %#v, want Hold", writes)
	}
}

func TestForegroundRealtimeContentionStillRejects(t *testing.T) {
	c, _ := newAdmissionPriorityController(t)
	holdRealtimeAdmission(t, c, admissionRealtime)

	if err := c.Action(context.Background(), grbl.ActionResume); !errors.Is(err, ErrControllerIOActive) {
		t.Fatalf("Action(Resume) error = %v, want %v", err, ErrControllerIOActive)
	}
	waitForForegroundAdmissionWaiters(t, c, 0)
	c.endRealtimeWrite()
}

func TestForegroundAdmissionCancellationDoesNotLeakWaiter(t *testing.T) {
	c, fake := newAdmissionPriorityController(t)
	holdRealtimeAdmission(t, c, admissionStatusPoll)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.writeManualLine(ctx, "G0 X1") }()
	waitForForegroundAdmissionWaiters(t, c, 1)
	cancel()
	if err := waitForErrorResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("writeManualLine() error = %v, want %v", err, context.Canceled)
	}
	waitForForegroundAdmissionWaiters(t, c, 0)
	c.endRealtimeWrite()

	if err := c.writeStatusPollForGeneration(context.Background(), fake.Generation()); err != nil {
		t.Fatalf("status poll after canceled waiter error = %v", err)
	}
}

func TestWaitingForegroundAdmissionPreventsNextStatusPoll(t *testing.T) {
	c, fake := newAdmissionPriorityController(t)
	holdRealtimeAdmission(t, c, admissionStatusPoll)

	ready := make(chan error, 1)
	go func() { ready <- c.beginForegroundAdmission(context.Background()) }()
	waitForForegroundAdmissionWaiters(t, c, 1)
	c.endRealtimeWrite()
	if err := waitForErrorResult(t, ready); err != nil {
		t.Fatalf("beginForegroundAdmission() error = %v", err)
	}
	waitForForegroundAdmissionWaiters(t, c, 1)

	if err := c.writeStatusPollForGeneration(context.Background(), fake.Generation()); !errors.Is(err, ErrControllerIOActive) {
		t.Fatalf("second status poll error = %v, want %v", err, ErrControllerIOActive)
	}
	c.endForegroundAdmission()
	waitForForegroundAdmissionWaiters(t, c, 0)
	if err := c.writeStatusPollForGeneration(context.Background(), fake.Generation()); err != nil {
		t.Fatalf("status poll after foreground handoff error = %v", err)
	}
}
