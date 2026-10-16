package frontend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/transport"
)

func TestLegacyProgramCoordinatorSerializesLoadAndRun(t *testing.T) {
	var c LegacyProgramCoordinator
	loadID, ok := c.BeginLoad()
	if !ok || !c.LoadPending() {
		t.Fatal("load was not registered")
	}
	if _, ok := c.BeginLoad(); ok {
		t.Fatal("overlapping load was accepted")
	}
	if _, ok := c.BeginRun(); ok {
		t.Fatal("run overtook pending load")
	}
	if !c.FinishLoad(loadID) || c.LoadPending() {
		t.Fatal("load did not finish")
	}
	runID, ok := c.BeginRun()
	if !ok || !c.RunPending() {
		t.Fatal("run was not registered")
	}
	if _, ok := c.BeginLoad(); ok {
		t.Fatal("load overtook accepted run")
	}
	if c.FinishRun(runID+1) || !c.RunPending() {
		t.Fatal("stale run result changed pending run")
	}
	if !c.FinishRun(runID) || c.RunPending() {
		t.Fatal("run did not finish")
	}
}

type legacyRequestHarness struct {
	controller  *app.Controller
	fake        *transport.FakeTransport
	coordinator LegacyProgramCoordinator
	gate        ProgramStartGate
	requests    LegacyProgramRequests
	loads       chan LegacyProgramLoadResult
	runs        chan LegacyProgramRunResult
}

func newLegacyRequestHarness(t *testing.T, load func(string) error, start func(context.Context) error) *legacyRequestHarness {
	t.Helper()
	fake := transport.NewFakeTransport()
	fake.SetResponding(false)
	controller := app.NewController(fake, nil)
	if err := controller.Connect(context.Background(), transport.DefaultPortConfig("legacy-request-test")); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	h := &legacyRequestHarness{
		controller: controller,
		fake:       fake,
		loads:      make(chan LegacyProgramLoadResult, 4),
		runs:       make(chan LegacyProgramRunResult, 4),
	}
	if load == nil {
		load = controller.LoadProgramFile
	}
	if start == nil {
		start = controller.StartProgram
	}
	h.requests = LegacyProgramRequests{
		Coordinator: &h.coordinator,
		StartGate:   &h.gate,
		Load:        load,
		Start:       start,
		OnLoadDone: func(result LegacyProgramLoadResult) {
			h.loads <- result
		},
		OnRunDone: func(result LegacyProgramRunResult) {
			h.runs <- result
		},
	}
	t.Cleanup(func() { _ = controller.Disconnect() })
	return h
}

func writeLegacyProgram(t *testing.T, name, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitLegacyResult[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for frontend operation result")
		var zero T
		return zero
	}
}

func waitLegacySignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitLegacyGateQuiescent(t *testing.T, gate *ProgramStartGate) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for !gate.Quiescent() {
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatalf("timed out waiting for start gate to become quiescent; outstanding=%d", gate.Outstanding())
		}
	}
}

// legacyOperationBarrier holds a test operation before it enters the real
// controller method. Cleanup always releases a reached barrier, so an earlier
// assertion failure cannot strand the corresponding request goroutine.
type legacyOperationBarrier struct {
	entered     chan struct{}
	release     chan struct{}
	done        chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
	doneOnce    sync.Once
	mu          sync.Mutex
	expected    bool
}

func newLegacyOperationBarrier(t *testing.T) *legacyOperationBarrier {
	t.Helper()
	b := &legacyOperationBarrier{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	t.Cleanup(func() {
		b.Release()
		b.mu.Lock()
		expected := b.expected
		b.mu.Unlock()
		if expected {
			select {
			case <-b.done:
			case <-time.After(2 * time.Second):
				t.Errorf("timed out waiting for delayed operation cleanup")
			}
		}
	})
	return b
}

// Expect must be called before the request that will enter this barrier. This
// lets cleanup release a worker that has been admitted but has not yet reached
// Block when an assertion fails.
func (b *legacyOperationBarrier) Expect() {
	b.mu.Lock()
	b.expected = true
	b.mu.Unlock()
}

func (b *legacyOperationBarrier) CancelExpectation() {
	b.mu.Lock()
	b.expected = false
	b.mu.Unlock()
}

func (b *legacyOperationBarrier) Block() {
	b.enterOnce.Do(func() { close(b.entered) })
	<-b.release
}

func (b *legacyOperationBarrier) Release() {
	b.releaseOnce.Do(func() { close(b.release) })
}

func (b *legacyOperationBarrier) Done() {
	b.doneOnce.Do(func() { close(b.done) })
}

func (b *legacyOperationBarrier) WaitEntered(t *testing.T, description string) {
	t.Helper()
	waitLegacySignal(t, b.entered, description)
}

func waitLegacyState(t *testing.T, controller *app.Controller, predicate func(app.State) bool) app.State {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		state := controller.Snapshot()
		if predicate(state) {
			return state
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatalf("timed out waiting for controller state: %+v", controller.Snapshot())
		}
	}
}

func waitLegacyWrite(t *testing.T, fake *transport.FakeTransport, display string) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		for _, message := range fake.Written() {
			if message.Display == display {
				return
			}
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatalf("timed out waiting for %q; writes=%#v", display, fake.Written())
		}
	}
}

func assertNoLegacyWrite(t *testing.T, fake *transport.FakeTransport, display string) {
	t.Helper()
	for _, message := range fake.Written() {
		if message.Display == display {
			t.Fatalf("unexpected program write %q", display)
		}
	}
}

func finishLegacyRun(t *testing.T, h *legacyRequestHarness) {
	t.Helper()
	h.fake.SetResponding(true)
	h.fake.InjectRX("ok")
	waitLegacyState(t, h.controller, func(state app.State) bool { return state.ProgramStatus == app.ProgramCompleted })
}

func TestLegacyProgramRequestsRunCannotBeOvertakenByLoad(t *testing.T) {
	programA := writeLegacyProgram(t, "a.gcode", "G0 X1\n")
	programB := writeLegacyProgram(t, "b.gcode", "G0 X2\n")
	var h *legacyRequestHarness
	var startBarrier *legacyOperationBarrier
	h = newLegacyRequestHarness(t, nil, func(ctx context.Context) error {
		defer startBarrier.Done()
		startBarrier.Block()
		return h.controller.StartProgram(ctx)
	})
	startBarrier = newLegacyOperationBarrier(t)
	if err := h.controller.LoadProgramFile(programA); err != nil {
		t.Fatalf("LoadProgramFile(A) error = %v", err)
	}
	if state := h.controller.Snapshot(); !state.LegacyProgramLoaded || state.LegacyProgramPath != programA {
		t.Fatalf("loaded A state = %+v", state)
	}
	startBarrier.Expect()
	if !h.requests.RequestRun() {
		startBarrier.CancelExpectation()
		t.Fatal("frontend rejected Run A")
	}
	startBarrier.WaitEntered(t, "delayed Run A controller start")
	if !h.coordinator.RunPending() || h.gate.Outstanding() != 1 {
		t.Fatalf("Run A was not registered before controller admission")
	}
	if h.requests.RequestLoad(programB) {
		t.Fatal("Load B overtook accepted Run A")
	}
	if state := h.controller.Snapshot(); state.LegacyProgramPath != programA {
		t.Fatalf("Load B altered retained legacy program: %+v", state)
	}
	startBarrier.Release()
	result := waitLegacyResult(t, h.runs)
	if result.Err != nil || !h.requests.FinishRun(result) {
		t.Fatalf("Run A result = %+v", result)
	}
	waitLegacyState(t, h.controller, func(state app.State) bool { return state.ProgramStatus == app.ProgramRunning })
	state := h.controller.Snapshot()
	if state.ProgramRunID == 0 || state.ProgramDocumentID != 0 || state.ProgramPath != programA {
		t.Fatalf("Run A identity = %+v", state)
	}
	waitLegacyWrite(t, h.fake, "G0 X1")
	assertNoLegacyWrite(t, h.fake, "G0 X2")
	waitLegacyState(t, h.controller, func(app.State) bool { return h.gate.Quiescent() })
	finishLegacyRun(t, h)
	if !h.requests.RequestLoad(programB) {
		t.Fatal("Load B remained blocked after Run A completed")
	}
	loadResult := waitLegacyResult(t, h.loads)
	if loadResult.Err != nil || !h.requests.FinishLoad(loadResult) {
		t.Fatalf("Load B result = %+v", loadResult)
	}
}

func TestLegacyProgramRequestsLoadPreventsRunOvertake(t *testing.T) {
	programA := writeLegacyProgram(t, "a.gcode", "G0 X1\n")
	programB := writeLegacyProgram(t, "b.gcode", "G0 X2\n")
	var h *legacyRequestHarness
	var loadBarrier *legacyOperationBarrier
	h = newLegacyRequestHarness(t, func(path string) error {
		defer loadBarrier.Done()
		loadBarrier.Block()
		return h.controller.LoadProgramFile(path)
	}, nil)
	loadBarrier = newLegacyOperationBarrier(t)
	if err := h.controller.LoadProgramFile(programA); err != nil {
		t.Fatalf("LoadProgramFile(A) error = %v", err)
	}
	loadBarrier.Expect()
	if !h.requests.RequestLoad(programB) {
		loadBarrier.CancelExpectation()
		t.Fatal("frontend rejected Load B")
	}
	loadBarrier.WaitEntered(t, "delayed Load B controller load")
	if h.requests.RequestRun() {
		t.Fatal("Run overtook pending Load B")
	}
	if h.gate.Outstanding() != 0 {
		t.Fatalf("rejected Run registered a start admission: %d", h.gate.Outstanding())
	}
	loadBarrier.Release()
	loadResult := waitLegacyResult(t, h.loads)
	if loadResult.Err != nil || !h.requests.FinishLoad(loadResult) {
		t.Fatalf("Load B result = %+v", loadResult)
	}
	if state := h.controller.Snapshot(); state.LegacyProgramPath != programB {
		t.Fatalf("loaded B state = %+v", state)
	}
	if !h.requests.RequestRun() {
		t.Fatal("Run B was not accepted after Load B")
	}
	runResult := waitLegacyResult(t, h.runs)
	if runResult.Err != nil || !h.requests.FinishRun(runResult) {
		t.Fatalf("Run B result = %+v", runResult)
	}
	waitLegacyWrite(t, h.fake, "G0 X2")
	assertNoLegacyWrite(t, h.fake, "G0 X1")
	finishLegacyRun(t, h)
}

func TestLegacyProgramRequestsFailedReplacementRetainsLoadedProgram(t *testing.T) {
	programA := writeLegacyProgram(t, "a.gcode", "G0 X1\n")
	missing := filepath.Join(t.TempDir(), "missing.gcode")
	h := newLegacyRequestHarness(t, nil, nil)
	if err := h.controller.LoadProgramFile(programA); err != nil {
		t.Fatalf("LoadProgramFile(A) error = %v", err)
	}
	if !h.requests.RequestLoad(missing) {
		t.Fatal("frontend rejected replacement load")
	}
	result := waitLegacyResult(t, h.loads)
	if result.Err == nil || !h.requests.FinishLoad(result) {
		t.Fatalf("failed load result = %+v", result)
	}
	if h.coordinator.LoadPending() {
		t.Fatal("failed load remained pending")
	}
	if state := h.controller.Snapshot(); state.LegacyProgramPath != programA || !state.LegacyProgramLoaded {
		t.Fatalf("failed replacement discarded A: %+v", state)
	}
	if !h.requests.RequestRun() {
		t.Fatal("Run A remained unavailable after failed replacement")
	}
	runResult := waitLegacyResult(t, h.runs)
	if runResult.Err != nil || !h.requests.FinishRun(runResult) {
		t.Fatalf("Run A result = %+v", runResult)
	}
	waitLegacyWrite(t, h.fake, "G0 X1")
	finishLegacyRun(t, h)
}

func TestLegacyProgramRequestsIgnoreStaleResults(t *testing.T) {
	var coordinator LegacyProgramCoordinator
	var gate ProgramStartGate
	loads := make(chan LegacyProgramLoadResult, 2)
	runs := make(chan LegacyProgramRunResult, 2)
	firstRunBarrier := newLegacyOperationBarrier(t)
	secondRunBarrier := newLegacyOperationBarrier(t)
	var startOrder struct {
		sync.Mutex
		count int
	}
	requests := LegacyProgramRequests{
		Coordinator: &coordinator,
		StartGate:   &gate,
		Load:        func(string) error { return nil },
		Start: func(context.Context) error {
			startOrder.Lock()
			startOrder.count++
			order := startOrder.count
			startOrder.Unlock()
			var barrier *legacyOperationBarrier
			switch order {
			case 1:
				barrier = firstRunBarrier
			case 2:
				barrier = secondRunBarrier
			default:
				return errors.New("unexpected Run request")
			}
			defer barrier.Done()
			barrier.Block()
			return errors.New("rejected")
		},
		OnLoadDone: func(result LegacyProgramLoadResult) { loads <- result },
		OnRunDone:  func(result LegacyProgramRunResult) { runs <- result },
	}
	if !requests.RequestLoad("first") {
		t.Fatal("first load rejected")
	}
	firstLoad := waitLegacyResult(t, loads)
	if !requests.FinishLoad(firstLoad) || !requests.RequestLoad("second") {
		t.Fatal("could not transition to second load")
	}
	if requests.FinishLoad(firstLoad) || !coordinator.LoadPending() {
		t.Fatal("stale load result cleared newer load")
	}
	secondLoad := waitLegacyResult(t, loads)
	if !requests.FinishLoad(secondLoad) {
		t.Fatal("second load did not finish")
	}
	firstRunBarrier.Expect()
	if !requests.RequestRun() {
		firstRunBarrier.CancelExpectation()
		t.Fatal("first run rejected")
	}
	firstRunBarrier.WaitEntered(t, "first delayed stale-result Run")
	firstRunBarrier.Release()
	firstRun := waitLegacyResult(t, runs)
	if !requests.FinishRun(firstRun) {
		t.Fatal("could not finish first run")
	}
	waitLegacyGateQuiescent(t, &gate)
	secondRunBarrier.Expect()
	if !requests.RequestRun() || !coordinator.RunPending() || gate.Outstanding() != 1 {
		secondRunBarrier.CancelExpectation()
		t.Fatal("could not transition to second run")
	}
	secondRunBarrier.WaitEntered(t, "second delayed stale-result Run")
	if requests.FinishRun(firstRun) || !coordinator.RunPending() {
		t.Fatal("stale run result changed newer request")
	}
	secondRunBarrier.Release()
	secondRun := waitLegacyResult(t, runs)
	if !requests.FinishRun(secondRun) {
		t.Fatal("second run did not finish")
	}
	waitLegacyGateQuiescent(t, &gate)
}

func TestLegacyProgramRequestsRunRemainsVisibleWhenShutdownFreezesGate(t *testing.T) {
	programA := writeLegacyProgram(t, "a.gcode", "G0 X1\n")
	var startCalls struct {
		sync.Mutex
		count int
	}
	var h *legacyRequestHarness
	var startBarrier *legacyOperationBarrier
	h = newLegacyRequestHarness(t, nil, func(ctx context.Context) error {
		startCalls.Lock()
		startCalls.count++
		startCalls.Unlock()
		defer startBarrier.Done()
		startBarrier.Block()
		return h.controller.StartProgram(ctx)
	})
	startBarrier = newLegacyOperationBarrier(t)
	if err := h.controller.LoadProgramFile(programA); err != nil {
		t.Fatalf("LoadProgramFile(A) error = %v", err)
	}
	startBarrier.Expect()
	if !h.requests.RequestRun() {
		startBarrier.CancelExpectation()
		t.Fatal("Run A rejected")
	}
	startBarrier.WaitEntered(t, "delayed Run A controller start")
	h.gate.Freeze()
	if h.gate.Quiescent() || !h.coordinator.RunPending() {
		t.Fatal("shutdown gate lost delayed Run A")
	}
	startBarrier.Release()
	result := waitLegacyResult(t, h.runs)
	if result.Err != nil || !h.requests.FinishRun(result) {
		t.Fatalf("Run A result = %+v", result)
	}
	waitLegacyState(t, h.controller, func(state app.State) bool {
		return state.ProgramStatus == app.ProgramRunning && state.ProgramDocumentID == 0 && state.ProgramPath == programA
	})
	waitLegacyGateQuiescent(t, &h.gate)
	if h.coordinator.RunPending() {
		t.Fatal("Run A coordinator ownership remained after its result")
	}
	// The coordinator is now idle, so this rejection can only be the frozen
	// shared gate. RequestRun rolls its temporary coordinator reservation back.
	if h.requests.RequestRun() {
		t.Fatal("frozen gate admitted a new Run")
	}
	startCalls.Lock()
	gotStartCalls := startCalls.count
	startCalls.Unlock()
	if gotStartCalls != 1 || h.coordinator.RunPending() || !h.gate.Quiescent() {
		t.Fatalf("frozen gate request changed frontend state: starts=%d pending=%v outstanding=%d", gotStartCalls, h.coordinator.RunPending(), h.gate.Outstanding())
	}
	finishLegacyRun(t, h)
}
