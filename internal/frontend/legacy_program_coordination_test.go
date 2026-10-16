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
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	var once sync.Once
	var h *legacyRequestHarness
	h = newLegacyRequestHarness(t, nil, func(ctx context.Context) error {
		once.Do(func() { close(startEntered) })
		<-releaseStart
		return h.controller.StartProgram(ctx)
	})
	if err := h.controller.LoadProgramFile(programA); err != nil {
		t.Fatalf("LoadProgramFile(A) error = %v", err)
	}
	if state := h.controller.Snapshot(); !state.LegacyProgramLoaded || state.LegacyProgramPath != programA {
		t.Fatalf("loaded A state = %+v", state)
	}
	if !h.requests.RequestRun() {
		t.Fatal("frontend rejected Run A")
	}
	<-startEntered
	if !h.coordinator.RunPending() || h.gate.Outstanding() != 1 {
		t.Fatalf("Run A was not registered before controller admission")
	}
	if h.requests.RequestLoad(programB) {
		t.Fatal("Load B overtook accepted Run A")
	}
	if state := h.controller.Snapshot(); state.LegacyProgramPath != programA {
		t.Fatalf("Load B altered retained legacy program: %+v", state)
	}
	close(releaseStart)
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
	loadEntered := make(chan struct{})
	releaseLoad := make(chan struct{})
	var h *legacyRequestHarness
	h = newLegacyRequestHarness(t, func(path string) error {
		close(loadEntered)
		<-releaseLoad
		return h.controller.LoadProgramFile(path)
	}, nil)
	if err := h.controller.LoadProgramFile(programA); err != nil {
		t.Fatalf("LoadProgramFile(A) error = %v", err)
	}
	if !h.requests.RequestLoad(programB) {
		t.Fatal("frontend rejected Load B")
	}
	<-loadEntered
	if h.requests.RequestRun() {
		t.Fatal("Run overtook pending Load B")
	}
	if h.gate.Outstanding() != 0 {
		t.Fatalf("rejected Run registered a start admission: %d", h.gate.Outstanding())
	}
	close(releaseLoad)
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
	requests := LegacyProgramRequests{
		Coordinator: &coordinator,
		StartGate:   &gate,
		Load:        func(string) error { return nil },
		Start:       func(context.Context) error { return errors.New("rejected") },
		OnLoadDone:  func(result LegacyProgramLoadResult) { loads <- result },
		OnRunDone:   func(result LegacyProgramRunResult) { runs <- result },
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
	if !requests.RequestRun() {
		t.Fatal("first run rejected")
	}
	firstRun := waitLegacyResult(t, runs)
	if !requests.FinishRun(firstRun) || !requests.RequestRun() {
		t.Fatal("could not transition to second run")
	}
	if requests.FinishRun(firstRun) || !coordinator.RunPending() || gate.Outstanding() != 1 {
		t.Fatal("stale run result changed newer request")
	}
	secondRun := waitLegacyResult(t, runs)
	if !requests.FinishRun(secondRun) {
		t.Fatal("second run did not finish")
	}
}

func TestLegacyProgramRequestsRunRemainsVisibleWhenShutdownFreezesGate(t *testing.T) {
	programA := writeLegacyProgram(t, "a.gcode", "G0 X1\n")
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	var h *legacyRequestHarness
	h = newLegacyRequestHarness(t, nil, func(ctx context.Context) error {
		close(startEntered)
		<-releaseStart
		return h.controller.StartProgram(ctx)
	})
	if err := h.controller.LoadProgramFile(programA); err != nil {
		t.Fatalf("LoadProgramFile(A) error = %v", err)
	}
	if !h.requests.RequestRun() {
		t.Fatal("Run A rejected")
	}
	<-startEntered
	h.gate.Freeze()
	if h.gate.Quiescent() || h.requests.RequestRun() {
		t.Fatal("shutdown gate lost delayed Run A")
	}
	close(releaseStart)
	result := waitLegacyResult(t, h.runs)
	if result.Err != nil || !h.requests.FinishRun(result) {
		t.Fatalf("Run A result = %+v", result)
	}
	waitLegacyState(t, h.controller, func(state app.State) bool {
		return state.ProgramStatus == app.ProgramRunning && state.ProgramDocumentID == 0 && state.ProgramPath == programA
	})
	waitLegacyState(t, h.controller, func(app.State) bool { return h.gate.Quiescent() })
	finishLegacyRun(t, h)
}
