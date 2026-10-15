package app

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ianbruene/ddgo/internal/gcode"
	"github.com/ianbruene/ddgo/internal/transport"
)

func newConnectedDocumentProgramController(t *testing.T) (*Controller, *transport.FakeTransport) {
	t.Helper()
	fake := transport.NewFakeTransport()
	fake.SetResponding(false)
	controller := NewController(fake, nil)
	controller.statusPollInterval = time.Hour
	if err := controller.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	drainEvents(controller.Events())
	return controller, fake
}

func documentProgram(t *testing.T, name, source string) gcode.Program {
	t.Helper()
	program, err := gcode.ProgramFromDocument(gcode.Document{Name: name, Source: source})
	if err != nil {
		t.Fatalf("ProgramFromDocument() error = %v", err)
	}
	return program
}

func TestStartDocumentProgramUsesAtomicSnapshotAndRetainsIdentity(t *testing.T) {
	controller, fake := newConnectedDocumentProgramController(t)
	legacyPath := writeProgramFile(t, "legacy.gcode", "M5\n")
	if err := controller.LoadProgramFile(legacyPath); err != nil {
		t.Fatalf("LoadProgramFile() error = %v", err)
	}
	drainEvents(controller.Events())

	program := documentProgram(t, "document.gcode", "G0 X1\nG0 Y2\n")
	documentID := DocumentID(41)
	runID, err := controller.StartDocumentProgram(context.Background(), documentID, program)
	if err != nil {
		t.Fatalf("StartDocumentProgram() error = %v", err)
	}
	if runID == 0 {
		t.Fatal("RunID = 0, want nonzero")
	}
	startEvent := waitForEventText(t, controller.Events(), EventStateChanged, "started program document.gcode")
	if startEvent.State.ProgramDocumentID != documentID || startEvent.State.ProgramRunID != runID {
		t.Fatalf("start identity = document %d run %d, want document %d run %d", startEvent.State.ProgramDocumentID, startEvent.State.ProgramRunID, documentID, runID)
	}
	if startEvent.State.ProgramName != "document.gcode" || startEvent.State.ProgramPath != "" || startEvent.State.ProgramTotal != 2 {
		t.Fatalf("start program state = %+v", startEvent.State)
	}

	waitForLineWrites(t, fake, "G0 X1")
	program.Lines[1] = gcode.Line{Number: 2, Raw: "G0 Y99", Text: "G0 Y99"}
	program.Lines = append(program.Lines, gcode.Line{Number: 3, Raw: "M3", Text: "M3"})
	fake.InjectRX("ok")
	waitForLineWrites(t, fake, "G0 X1", "G0 Y2")
	progress := waitForState(t, controller, func(state State) bool { return state.ProgramComplete == 1 })
	if progress.ProgramDocumentID != documentID || progress.ProgramRunID != runID || progress.ProgramTotal != 2 {
		t.Fatalf("progress state lost snapshot identity: %+v", progress)
	}

	fake.InjectRX("ok")
	completed := waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
	if completed.ProgramDocumentID != documentID || completed.ProgramRunID != runID || completed.ProgramComplete != 2 {
		t.Fatalf("completion state = %+v", completed)
	}
	completionEvent := waitForEventText(t, controller.Events(), EventStateChanged, "program document.gcode completed")
	if completionEvent.State.ProgramDocumentID != documentID || completionEvent.State.ProgramRunID != runID {
		t.Fatalf("completion event identity = document %d run %d", completionEvent.State.ProgramDocumentID, completionEvent.State.ProgramRunID)
	}
}

func TestDocumentProgramRunIDsAreUniqueAndRevisionsDistinguishRuns(t *testing.T) {
	controller, fake := newConnectedDocumentProgramController(t)
	program := documentProgram(t, "repeat.gcode", "M5\n")

	firstID, err := controller.StartDocumentProgram(context.Background(), 7, program)
	if err != nil {
		t.Fatalf("first StartDocumentProgram() error = %v", err)
	}
	firstStart := waitForEventText(t, controller.Events(), EventStateChanged, "started program repeat.gcode")
	waitForLineWrites(t, fake, "M5")
	fake.InjectRX("ok")
	firstComplete := waitForEventText(t, controller.Events(), EventStateChanged, "program repeat.gcode completed")

	secondID, err := controller.StartDocumentProgram(context.Background(), 7, program)
	if err != nil {
		t.Fatalf("second StartDocumentProgram() error = %v", err)
	}
	secondStart := waitForEventText(t, controller.Events(), EventStateChanged, "started program repeat.gcode")
	if firstID == 0 || secondID == 0 || firstID == secondID {
		t.Fatalf("RunIDs = %d, %d, want distinct nonzero values", firstID, secondID)
	}
	if firstStart.State.ProgramRunID != firstID || firstComplete.State.ProgramRunID != firstID || secondStart.State.ProgramRunID != secondID {
		t.Fatalf("event RunIDs = first start %d first complete %d second start %d", firstStart.State.ProgramRunID, firstComplete.State.ProgramRunID, secondStart.State.ProgramRunID)
	}
	if secondStart.StateRevision <= firstComplete.StateRevision {
		t.Fatalf("second start revision = %d, want greater than first completion %d", secondStart.StateRevision, firstComplete.StateRevision)
	}

	waitForLineWrites(t, fake, "M5", "M5")
	fake.InjectRX("ok")
	waitForState(t, controller, func(state State) bool {
		return state.ProgramStatus == ProgramCompleted && state.ProgramRunID == secondID
	})
}

func TestConcurrentDocumentStartsAdmitOnlyOneRun(t *testing.T) {
	controller, fake := newConnectedDocumentProgramController(t)
	type result struct {
		documentID DocumentID
		runID      RunID
		err        error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, candidate := range []struct {
		documentID DocumentID
		program    gcode.Program
	}{
		{11, documentProgram(t, "first.gcode", "G0 X1\n")},
		{22, documentProgram(t, "second.gcode", "G0 X2\n")},
	} {
		candidate := candidate
		go func() {
			ready.Done()
			<-start
			runID, err := controller.StartDocumentProgram(context.Background(), candidate.documentID, candidate.program)
			results <- result{documentID: candidate.documentID, runID: runID, err: err}
		}()
	}
	ready.Wait()
	close(start)
	first := <-results
	second := <-results

	var admitted, rejected result
	if first.err == nil {
		admitted, rejected = first, second
	} else {
		admitted, rejected = second, first
	}
	if admitted.err != nil || admitted.runID == 0 {
		t.Fatalf("admitted result = %+v", admitted)
	}
	if rejected.err == nil || rejected.runID != 0 {
		t.Fatalf("rejected result = %+v, want error and zero RunID", rejected)
	}
	state := controller.Snapshot()
	if state.ProgramDocumentID != admitted.documentID || state.ProgramRunID != admitted.runID || state.ProgramStatus != ProgramRunning {
		t.Fatalf("active state overwritten by rejected start: %+v; admitted=%+v rejected=%+v", state, admitted, rejected)
	}
	controller.mu.RLock()
	activeProgram := controller.run.program
	ownerKind := controller.responseOwner.kind
	controller.mu.RUnlock()
	wantName := "first.gcode"
	wantLine := "G0 X1"
	if admitted.documentID == 22 {
		wantName = "second.gcode"
		wantLine = "G0 X2"
	}
	if activeProgram.Name != wantName || ownerKind != responseOwnerProgram {
		t.Fatalf("active program = %q owner = %v, want %q/program", activeProgram.Name, ownerKind, wantName)
	}
	waitForLineWrites(t, fake, wantLine)
	fake.InjectRX("ok")
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
}

func TestInvalidDocumentProgramsCannotAcquireRunOwnership(t *testing.T) {
	controller, fake := newConnectedDocumentProgramController(t)
	valid := documentProgram(t, "valid.gcode", "M5\n")

	if runID, err := controller.StartDocumentProgram(context.Background(), 0, valid); !errors.Is(err, ErrDocumentIDRequired) || runID != 0 {
		t.Fatalf("zero-document start = run %d error %v", runID, err)
	}
	invalid := []gcode.Program{
		{},
		{Name: "blank.gcode", Lines: []gcode.Line{{Number: 1, Text: ""}}},
		{Name: "multi-line.gcode", Lines: []gcode.Line{{Number: 1, Text: "G0 X1\nG0 X2"}}},
	}
	for _, program := range invalid {
		if runID, err := controller.StartDocumentProgram(context.Background(), 1, program); err == nil || runID != 0 {
			t.Fatalf("invalid start for %#v = run %d error %v", program, runID, err)
		}
	}
	controller.mu.RLock()
	if controller.run != nil || controller.responseOwner.kind != responseOwnerNone {
		controller.mu.RUnlock()
		t.Fatal("invalid program acquired run or response ownership")
	}
	controller.mu.RUnlock()

	runID, err := controller.StartDocumentProgram(context.Background(), 1, valid)
	if err != nil {
		t.Fatalf("valid StartDocumentProgram() error = %v", err)
	}
	if runID != 1 {
		t.Fatalf("first admitted RunID = %d, want 1", runID)
	}
	waitForLineWrites(t, fake, "M5")
	fake.InjectRX("ok")
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
}

func TestDocumentRunIdentitySurvivesPauseResumeFailureAndStop(t *testing.T) {
	controller, fake := newConnectedDocumentProgramController(t)
	program := documentProgram(t, "lifecycle.gcode", "G0 X1\nG0 X2\n")
	documentID := DocumentID(91)
	runID, err := controller.StartDocumentProgram(context.Background(), documentID, program)
	if err != nil {
		t.Fatalf("StartDocumentProgram() error = %v", err)
	}
	waitForLineWrites(t, fake, "G0 X1")

	if err := controller.PauseProgram(context.Background()); err != nil {
		t.Fatalf("PauseProgram() error = %v", err)
	}
	pauseEvent := waitForEventText(t, controller.Events(), EventStateChanged, "program paused")
	paused := pauseEvent.State
	if paused.ProgramDocumentID != documentID || paused.ProgramRunID != runID {
		t.Fatalf("paused identity = %+v", paused)
	}
	if err := controller.ResumeProgram(context.Background()); err != nil {
		t.Fatalf("ResumeProgram() error = %v", err)
	}
	resumeEvent := waitForEventText(t, controller.Events(), EventStateChanged, "program resumed")
	resumed := resumeEvent.State
	if resumed.ProgramDocumentID != documentID || resumed.ProgramRunID != runID {
		t.Fatalf("resumed identity = %+v", resumed)
	}
	fake.InjectRX("error:1")
	failureEvent := waitForEventText(t, controller.Events(), EventStateChanged, "program failed")
	failed := failureEvent.State
	if failed.ProgramDocumentID != documentID || failed.ProgramRunID != runID {
		t.Fatalf("failed identity = %+v", failed)
	}

	stopRunID, err := controller.StartDocumentProgram(context.Background(), documentID, program)
	if err != nil {
		t.Fatalf("replacement StartDocumentProgram() error = %v", err)
	}
	if stopRunID == runID {
		t.Fatalf("replacement RunID = %d, want different from %d", stopRunID, runID)
	}
	waitForLineWrites(t, fake, "G0 X1", "G0 X1")
	if err := controller.StopProgram(context.Background()); err != nil {
		t.Fatalf("StopProgram() error = %v", err)
	}
	stopEvent := waitForEventText(t, controller.Events(), EventStateChanged, "program stopped")
	stopped := stopEvent.State
	if stopped.ProgramDocumentID != documentID || stopped.ProgramRunID != stopRunID {
		t.Fatalf("stopped identity = %+v", stopped)
	}
}

func TestLegacyProgramRunsReceiveIdentityAndLoadCommitRevalidates(t *testing.T) {
	controller, fake := newConnectedDocumentProgramController(t)
	firstPath := writeProgramFile(t, "first-legacy.gcode", "G0 X1\n")
	if err := controller.LoadProgramFile(firstPath); err != nil {
		t.Fatalf("LoadProgramFile() error = %v", err)
	}
	candidate := documentProgram(t, "second-legacy.gcode", "G0 X9\n")
	loadEntered := make(chan struct{})
	releaseLoad := make(chan struct{})
	loadResult := make(chan error, 1)
	go func() {
		loadResult <- controller.loadProgramFile("second-legacy.gcode", func(string) (gcode.Program, error) {
			close(loadEntered)
			<-releaseLoad
			return candidate, nil
		})
	}()
	<-loadEntered
	if state := controller.Snapshot(); state.ProgramDocumentID != 0 || state.ProgramRunID != 0 || state.ProgramStatus != ProgramLoaded {
		t.Fatalf("loaded legacy state = %+v", state)
	}
	if err := controller.StartProgram(context.Background()); err != nil {
		t.Fatalf("StartProgram() error = %v", err)
	}
	active := controller.Snapshot()
	if active.ProgramDocumentID != 0 || active.ProgramRunID == 0 || active.ProgramName != "first-legacy.gcode" {
		t.Fatalf("active legacy state = %+v", active)
	}

	// Allow the load that began before StartProgram to finish its file I/O. Its
	// locked commit must observe the admitted run and reject the replacement.
	close(releaseLoad)
	if err := <-loadResult; !errors.Is(err, ErrProgramActive) {
		t.Fatalf("concurrent load error = %v, want %v", err, ErrProgramActive)
	}
	controller.mu.RLock()
	loaded := controller.loaded
	controller.mu.RUnlock()
	if loaded.Name != "first-legacy.gcode" || !reflect.DeepEqual(loaded.Lines, []gcode.Line{{Number: 1, Raw: "G0 X1", Text: "G0 X1"}}) {
		t.Fatalf("loaded program replaced during active run: %#v", loaded)
	}
	if state := controller.Snapshot(); state.ProgramRunID != active.ProgramRunID || state.ProgramDocumentID != 0 || state.ProgramName != active.ProgramName {
		t.Fatalf("active attribution changed after rejected load: %+v, want run %d name %q", state, active.ProgramRunID, active.ProgramName)
	}

	waitForLineWrites(t, fake, "G0 X1")
	fake.InjectRX("ok")
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
	if err := controller.LoadProgramFile(writeProgramFile(t, "replacement.gcode", "M5\n")); err != nil {
		t.Fatalf("replacement LoadProgramFile() error = %v", err)
	}
	reloaded := controller.Snapshot()
	if reloaded.ProgramStatus != ProgramLoaded || reloaded.ProgramDocumentID != 0 || reloaded.ProgramRunID != 0 || reloaded.ProgramName != "replacement.gcode" {
		t.Fatalf("replacement loaded state = %+v", reloaded)
	}
	if err := controller.StartProgram(context.Background()); err != nil {
		t.Fatalf("replacement StartProgram() error = %v", err)
	}
	second := controller.Snapshot()
	if second.ProgramRunID == 0 || second.ProgramRunID == active.ProgramRunID || second.ProgramDocumentID != 0 {
		t.Fatalf("replacement legacy run identity = %+v", second)
	}
	waitForLineWrites(t, fake, "G0 X1", "M5")
	fake.InjectRX("ok")
	waitForState(t, controller, func(state State) bool { return state.ProgramStatus == ProgramCompleted })
}
