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
