package frontend

import (
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/gcode"
)

func TestExecutionSummaryNoExecutionIgnoresLoadedLegacyProgram(t *testing.T) {
	summary := ExecutionSummaryForState(app.State{
		ProgramStatus:       app.ProgramLoaded,
		ProgramName:         "legacy.gcode",
		LegacyProgramLoaded: true,
		LegacyProgramTotal:  4,
	})
	if summary.RunID != 0 || summary.Name != "No program running" || summary.Status != "No execution yet" {
		t.Fatalf("loaded legacy program became execution summary: %+v", summary)
	}
	if CanFocusExecutionSummary(app.State{}, summary.DocumentID, summary.RunID, true) {
		t.Fatal("no execution became focusable")
	}
}

func TestExecutionSummaryPresentsActiveAndTerminalDocumentRuns(t *testing.T) {
	running := app.State{
		ProgramName:       "a.gcode",
		ProgramDocumentID: 7,
		ProgramRunID:      10,
		ProgramStatus:     app.ProgramRunning,
		ProgramComplete:   3,
		ProgramTotal:      8,
	}
	summary := ExecutionSummaryForState(running)
	if summary.Heading != "Current Execution" || summary.Name != "a.gcode" || summary.Status != "Running" || summary.Progress != "3 / 8 commands acknowledged" {
		t.Fatalf("running summary = %+v", summary)
	}
	if !CanFocusExecutionSummary(running, 7, 10, true) || CanFocusExecutionSummary(running, 7, 10, false) {
		t.Fatal("running focus eligibility was incorrect")
	}

	completed := running
	completed.ProgramStatus = app.ProgramCompleted
	completed.ProgramComplete = 8
	summary = ExecutionSummaryForState(completed)
	if summary.Heading != "Last Execution" || summary.Status != "Completed" || summary.Complete != 8 || summary.Detail == "" {
		t.Fatalf("completed summary = %+v", summary)
	}
}

func TestExecutionSummaryTerminalStatesRetainProgressWithoutPendingExecution(t *testing.T) {
	for _, status := range []app.ProgramStatus{app.ProgramDraining, app.ProgramStopping, app.ProgramStopped, app.ProgramCompleted, app.ProgramFailed} {
		t.Run(string(status), func(t *testing.T) {
			summary := ExecutionSummaryForState(app.State{
				ProgramName:       "a.gcode",
				ProgramDocumentID: 7,
				ProgramRunID:      10,
				ProgramStatus:     status,
				ProgramComplete:   4,
				ProgramTotal:      9,
				ProgramError:      "line rejected",
			})
			if summary.Complete != 4 || summary.Total != 9 || summary.Progress != "4 / 9 commands acknowledged" {
				t.Fatalf("terminal progress = %+v", summary)
			}
			if status == app.ProgramFailed && summary.Detail != "line rejected" {
				t.Fatalf("failed summary lost run error: %+v", summary)
			}
		})
	}
}

func TestExecutionSummaryFocusRequiresExactCurrentIdentity(t *testing.T) {
	stateA := app.State{ProgramDocumentID: 7, ProgramRunID: 10, ProgramStatus: app.ProgramCompleted}
	if !CanFocusExecutionSummary(stateA, 7, 10, true) {
		t.Fatal("matching document execution was not focusable")
	}
	stateB := app.State{ProgramDocumentID: 8, ProgramRunID: 11, ProgramStatus: app.ProgramRunning}
	if CanFocusExecutionSummary(stateB, 7, 10, true) {
		t.Fatal("stale A focus target matched B execution")
	}
	if CanFocusExecutionSummary(stateA, 0, 10, true) {
		t.Fatal("legacy execution was focusable")
	}
}

func TestExecutionSummaryPresentsLegacyRunWithoutDocumentFocus(t *testing.T) {
	state := app.State{
		ProgramName:       "compatibility.gcode",
		ProgramDocumentID: 0,
		ProgramRunID:      10,
		ProgramStatus:     app.ProgramPaused,
		ProgramComplete:   2,
		ProgramTotal:      5,
	}
	summary := ExecutionSummaryForState(state)
	if summary.Name != "compatibility.gcode" || summary.Status != "Paused" || summary.Progress != "2 / 5 commands acknowledged" {
		t.Fatalf("legacy execution summary = %+v", summary)
	}
	if CanFocusExecutionSummary(state, 0, 10, true) {
		t.Fatal("legacy execution exposed a document focus target")
	}
}

func TestExecutionSummaryClosedAndReopenedDocumentIdentity(t *testing.T) {
	state := app.State{ProgramDocumentID: 7, ProgramRunID: 10, ProgramStatus: app.ProgramCompleted}
	if CanFocusExecutionSummary(state, 7, 10, false) {
		t.Fatal("closed document remained focusable")
	}
	// The same path reopened as DocumentID 8 cannot satisfy the historical run.
	if CanFocusExecutionSummary(state, 8, 10, true) {
		t.Fatal("reopened replacement document matched historical execution")
	}
}

func TestExecutionSummaryFocusTracksRegistryLifecycle(t *testing.T) {
	registry, _ := newTestDocumentRegistry(t)
	path := writeDocument(t, "summary.gcode", "G0 X1\n")
	var windows []*fakeDocumentWindow
	first, opened, err := registry.OpenFile(path, gcode.LoadDocument, testDocumentWindowFactory(&windows))
	if err != nil || !opened {
		t.Fatalf("OpenFile(first) = (%v, %v)", opened, err)
	}
	state := app.State{ProgramDocumentID: first.ID, ProgramRunID: 10, ProgramStatus: app.ProgramCompleted}
	_, open := registry.Entry(first.ID)
	if !CanFocusExecutionSummary(state, first.ID, 10, open) {
		t.Fatal("open source document was not focusable")
	}
	first.Window.Close()
	_, open = registry.Entry(first.ID)
	if CanFocusExecutionSummary(state, first.ID, 10, open) {
		t.Fatal("closed source document remained focusable")
	}
	second, opened, err := registry.OpenFile(path, gcode.LoadDocument, testDocumentWindowFactory(&windows))
	if err != nil || !opened || second.ID == first.ID {
		t.Fatalf("OpenFile(reopen) = id=%d opened=%v err=%v", second.ID, opened, err)
	}
	_, open = registry.Entry(second.ID)
	if CanFocusExecutionSummary(state, second.ID, 10, open) {
		t.Fatal("historical run focused reopened replacement document")
	}
}

type executionSummaryView struct{ summary ExecutionSummary }

func (v *executionSummaryView) ApplyState(state app.State) {
	v.summary = ExecutionSummaryForState(state)
}
func (v *executionSummaryView) ApplyEvent(app.Event) {}

func TestExecutionSummaryUsesNewestDispatchedState(t *testing.T) {
	old := app.State{ProgramName: "a.gcode", ProgramDocumentID: 7, ProgramRunID: 10, ProgramStatus: app.ProgramRunning, ProgramComplete: 1, ProgramTotal: 4}
	newer := old
	newer.ProgramName = "b.gcode"
	newer.ProgramDocumentID = 8
	newer.ProgramRunID = 11
	newer.ProgramComplete = 3
	dispatcher := NewViewDispatcher(old, 1)
	view := &executionSummaryView{}
	dispatcher.Register(view)
	dispatcher.Dispatch(app.Event{State: newer, StateRevision: 3})
	dispatcher.Dispatch(app.Event{State: old, StateRevision: 2})
	if view.summary.DocumentID != 8 || view.summary.RunID != 11 || view.summary.Name != "b.gcode" || view.summary.Complete != 3 {
		t.Fatalf("stale state regressed execution summary: %+v", view.summary)
	}
}

func TestExecutionSummaryLifecycleRefreshDoesNotBypassDispatcherRevision(t *testing.T) {
	state10 := app.State{
		ProgramName:       "a.gcode",
		ProgramDocumentID: 7,
		ProgramRunID:      10,
		ProgramStatus:     app.ProgramRunning,
		ProgramComplete:   1,
		ProgramTotal:      4,
	}
	state11 := state10
	state11.ProgramComplete = 2
	state12 := app.State{
		ProgramName:       "b.gcode",
		ProgramDocumentID: 8,
		ProgramRunID:      11,
		ProgramStatus:     app.ProgramRunning,
		ProgramComplete:   3,
		ProgramTotal:      5,
	}

	dispatcher := NewViewDispatcher(state10, 10)
	view := &executionSummaryView{}
	dispatcher.Register(view)

	// A document lifecycle callback may consult a fresh controller snapshot to
	// validate focus, but it must not apply that snapshot to the summary.
	if CanFocusDisplayedExecutionSummary(view.summary, func(documentID app.DocumentID, runID app.RunID) bool {
		return CanFocusExecutionSummary(state12, documentID, runID, true)
	}) {
		t.Fatal("newer undispatched execution unexpectedly matched displayed focus target")
	}
	if view.summary.DocumentID != 7 || view.summary.RunID != 10 || view.summary.Complete != 1 {
		t.Fatalf("lifecycle focus validation changed undispatched summary: %+v", view.summary)
	}

	dispatcher.Dispatch(app.Event{State: state11, StateRevision: 11})
	if view.summary.DocumentID != 7 || view.summary.RunID != 10 || view.summary.Complete != 2 {
		t.Fatalf("revision 11 summary = %+v", view.summary)
	}
	dispatcher.Dispatch(app.Event{State: state12, StateRevision: 12})
	if view.summary.DocumentID != 8 || view.summary.RunID != 11 || view.summary.Complete != 3 {
		t.Fatalf("revision 12 summary = %+v", view.summary)
	}
}

func TestExecutionSummaryLifecycleFocusAvailabilityDoesNotReplaceHistory(t *testing.T) {
	state := app.State{
		ProgramName:       "a.gcode",
		ProgramDocumentID: 7,
		ProgramRunID:      10,
		ProgramStatus:     app.ProgramCompleted,
		ProgramComplete:   4,
		ProgramTotal:      4,
	}
	summary := ExecutionSummaryForState(state)
	if !CanFocusDisplayedExecutionSummary(summary, func(documentID app.DocumentID, runID app.RunID) bool {
		return CanFocusExecutionSummary(state, documentID, runID, true)
	}) {
		t.Fatal("open document was not focusable")
	}

	// Closing the document changes only focus eligibility; the historical
	// controller presentation remains exactly the state accepted earlier.
	if CanFocusDisplayedExecutionSummary(summary, func(documentID app.DocumentID, runID app.RunID) bool {
		return CanFocusExecutionSummary(state, documentID, runID, false)
	}) {
		t.Fatal("closed document remained focusable")
	}
	if summary.Name != "a.gcode" || summary.Status != "Completed" || summary.Complete != 4 || summary.DocumentID != 7 || summary.RunID != 10 {
		t.Fatalf("lifecycle focus update changed historical summary: %+v", summary)
	}

	// A same-path replacement has a distinct DocumentID and cannot become the
	// focus target for the old execution.
	if CanFocusDisplayedExecutionSummary(summary, func(documentID app.DocumentID, runID app.RunID) bool {
		return CanFocusExecutionSummary(state, 8, runID, true)
	}) {
		t.Fatal("reopened document became focusable for historical execution")
	}
}

func TestExecutionSummaryStaleFocusValidationDoesNotChangeDisplayedRun(t *testing.T) {
	stateA := app.State{ProgramName: "a.gcode", ProgramDocumentID: 7, ProgramRunID: 10, ProgramStatus: app.ProgramCompleted}
	summary := ExecutionSummaryForState(stateA)
	stateB := app.State{ProgramName: "b.gcode", ProgramDocumentID: 8, ProgramRunID: 11, ProgramStatus: app.ProgramRunning}
	if CanFocusDisplayedExecutionSummary(summary, func(documentID app.DocumentID, runID app.RunID) bool {
		return CanFocusExecutionSummary(stateB, documentID, runID, true)
	}) {
		t.Fatal("stale focus request matched replacement execution")
	}
	if summary.DocumentID != 7 || summary.RunID != 10 || summary.Name != "a.gcode" {
		t.Fatalf("stale focus validation changed displayed execution: %+v", summary)
	}
}
