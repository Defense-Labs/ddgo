package frontend

import (
	"errors"
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/gcode"
)

func TestDocumentExecutionPresentationMapsPhysicalLinesAndRetainsHistory(t *testing.T) {
	program, err := gcode.ProgramFromDocument(gcode.Document{Source: "; comment\nG0 X1\n\n(remark)\nG1 X2\n"})
	if err != nil {
		t.Fatal(err)
	}
	presentation := NewDocumentExecutionPresentation(7, program)
	presentation.ApplyState(app.State{ProgramDocumentID: 7, ProgramRunID: 3, ProgramStatus: app.ProgramRunning, ProgramTotal: 2, ProgramComplete: 0})
	if presentation.NextLine != 2 {
		t.Fatalf("next line = %d, want 2", presentation.NextLine)
	}
	presentation.ApplyState(app.State{ProgramDocumentID: 7, ProgramRunID: 3, ProgramStatus: app.ProgramDraining, ProgramTotal: 2, ProgramComplete: 2})
	if presentation.NextLine != 0 || presentation.Message == "" {
		t.Fatalf("draining presentation = %+v", presentation)
	}
	// Another document's state leaves this document's finished presentation intact.
	presentation.ApplyState(app.State{ProgramDocumentID: 8, ProgramRunID: 4, ProgramStatus: app.ProgramRunning})
	if presentation.RunID != 3 || presentation.Status != app.ProgramDraining {
		t.Fatalf("other document overwrote history: %+v", presentation)
	}
}

func TestDocumentExecutionPresentationClearsNextLineForTerminalStates(t *testing.T) {
	program := gcode.Program{Lines: []gcode.Line{{Number: 2, Text: "G0 X1"}, {Number: 5, Text: "G1 X2"}}}
	for _, status := range []app.ProgramStatus{app.ProgramDraining, app.ProgramStopping, app.ProgramStopped, app.ProgramCompleted, app.ProgramFailed} {
		t.Run(string(status), func(t *testing.T) {
			p := NewDocumentExecutionPresentation(7, program)
			p.ApplyState(app.State{ProgramDocumentID: 7, ProgramRunID: 11, ProgramStatus: status, ProgramTotal: 2, ProgramComplete: 1, ProgramError: "failure"})
			if p.NextLine != 0 || p.Complete != 1 {
				t.Fatalf("terminal presentation = %+v", p)
			}
		})
	}
	for _, status := range []app.ProgramStatus{app.ProgramRunning, app.ProgramPaused} {
		t.Run("active-"+string(status), func(t *testing.T) {
			p := NewDocumentExecutionPresentation(7, program)
			p.ApplyState(app.State{ProgramDocumentID: 7, ProgramRunID: 11, ProgramStatus: status, ProgramTotal: 2, ProgramComplete: 1})
			if p.NextLine != 5 {
				t.Fatalf("active presentation next line = %d, want 5", p.NextLine)
			}
		})
	}
}

func TestDocumentExecutionPresentationAttributesOnlyExactRunErrors(t *testing.T) {
	p := NewDocumentExecutionPresentation(7, gcode.Program{Lines: []gcode.Line{{Number: 1, Text: "G0 X1"}}})
	p.ApplyState(app.State{ProgramDocumentID: 7, ProgramRunID: 3, ProgramStatus: app.ProgramRunning, ProgramTotal: 1})
	p.ApplyEvent(app.Event{Kind: app.EventError, ErrorDocumentID: 7, ErrorRunID: 2, Err: errors.New("old")})
	if p.Error != "" {
		t.Fatalf("stale error applied: %q", p.Error)
	}
	p.ApplyEvent(app.Event{Kind: app.EventError, ErrorDocumentID: 8, ErrorRunID: 3, Err: errors.New("other")})
	if p.Error != "" {
		t.Fatalf("other document error applied: %q", p.Error)
	}
	p.ApplyEvent(app.Event{Kind: app.EventError, ErrorDocumentID: 7, ErrorRunID: 3, Err: errors.New("owned")})
	if p.Error != "owned" {
		t.Fatalf("owned error = %q", p.Error)
	}
}

func TestDocumentExecutionPresentationAdmissionResultCannotEraseFailure(t *testing.T) {
	p := NewDocumentExecutionPresentation(7, gcode.Program{Lines: []gcode.Line{{Number: 2, Text: "G0 X1"}}})
	p.ApplyState(app.State{ProgramDocumentID: 7, ProgramRunID: 11, ProgramStatus: app.ProgramRunning, ProgramTotal: 1})
	p.ApplyState(app.State{ProgramDocumentID: 7, ProgramRunID: 11, ProgramStatus: app.ProgramFailed, ProgramTotal: 1, ProgramComplete: 1, ProgramError: "controller rejected line"})
	p.ReconcileAdmission(11, nil)
	if p.RunID != 11 || p.Status != app.ProgramFailed || p.Error != "controller rejected line" || p.Complete != 1 {
		t.Fatalf("late admission overwrote failure: %+v", p)
	}

	// A stale completion for an older admission cannot replace a later run.
	p.ReconcileAdmission(10, nil)
	if p.RunID != 11 || p.Status != app.ProgramFailed || p.Error != "controller rejected line" {
		t.Fatalf("stale admission overwrote current run: %+v", p)
	}

	p.ReconcileAdmission(12, nil)
	if p.RunID != 12 || p.Status != app.ProgramRunning || p.Error != "" || p.NextLine != 2 {
		t.Fatalf("new admission did not replace old failure: %+v", p)
	}
}

func TestDocumentExecutionPresentationUnscopedErrorDoesNotReplaceRunFailure(t *testing.T) {
	p := NewDocumentExecutionPresentation(7, gcode.Program{Lines: []gcode.Line{{Number: 1, Text: "M5"}}})
	p.ApplyState(app.State{ProgramDocumentID: 7, ProgramRunID: 11, ProgramStatus: app.ProgramFailed, ProgramError: "run failure"})
	p.ApplyEvent(app.Event{Kind: app.EventError, Err: errors.New("general failure")})
	if p.Error != "run failure" {
		t.Fatalf("general error replaced run error: %q", p.Error)
	}
}

func TestDocumentExecutionControlsAreOwnerScoped(t *testing.T) {
	state := app.State{ConnectionStatus: app.ConnectionConnected, EStopStatus: app.EStopClear, MachineReadiness: app.MachineReadinessNormal, ProgramDocumentID: 7, ProgramRunID: 4, ProgramStatus: app.ProgramPaused}
	owner := DocumentExecutionControlState(7, true, false, false, state)
	other := DocumentExecutionControlState(8, true, false, false, state)
	if !owner.Resume || !owner.Stop || owner.Run || owner.Pause {
		t.Fatalf("owner controls = %+v", owner)
	}
	if other.Run || other.Pause || other.Resume || other.Stop {
		t.Fatalf("other controls = %+v", other)
	}
}

func TestSourceHighlightUpdate(t *testing.T) {
	tests := []struct {
		name                 string
		current, next        int
		follow, wasFollowing bool
		want                 SourceHighlightAction
	}{
		{"new line follows", 0, 2, true, true, SourceHighlightAction{Apply: true, Reveal: true}},
		{"unchanged line is inert", 2, 2, true, true, SourceHighlightAction{}},
		{"follow disabled updates only", 2, 5, false, false, SourceHighlightAction{Apply: true}},
		{"reenable reveals current", 5, 5, true, false, SourceHighlightAction{Apply: true, Reveal: true}},
		{"clear marker", 5, 0, true, true, SourceHighlightAction{Apply: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SourceHighlightUpdate(tt.current, tt.next, tt.follow, tt.wasFollowing); got != tt.want {
				t.Fatalf("SourceHighlightUpdate() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
