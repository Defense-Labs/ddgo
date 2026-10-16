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
