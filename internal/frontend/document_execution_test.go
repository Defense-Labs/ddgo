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

func TestPackageStepExecutionFeedback(t *testing.T) {
	ready := ExecutionSessionSnapshot{Controls: DocumentExecutionControls{Run: true}, State: app.State{ConnectionStatus: app.ConnectionConnected, EStopStatus: app.EStopClear, MachineReadiness: app.MachineReadinessNormal}}
	invalid := ready
	invalid.ParseError = errors.New("line 2: unsupported command")
	rejected := ready
	rejected.StartErr = errors.New("controller is not ready")
	disconnected := ready
	disconnected.Controls.Run = false
	disconnected.State.ConnectionStatus = app.ConnectionDisconnected
	estop := ready
	estop.Controls.Run = false
	estop.State.EStopStatus = app.EStopActive
	recovery := ready
	recovery.Controls.Run = false
	recovery.State.MachineReadiness = app.MachineReadinessRecoveryRequired
	pending := ready
	pending.Controls.Run = false
	pending.StartPending = true
	rejectedDisconnected := disconnected
	rejectedDisconnected.StartErr = errors.New("controller was not ready")
	active := ready
	active.Controls.Run = false
	active.State.ProgramStatus = app.ProgramRunning

	tests := []struct {
		name        string
		hasGCode    bool
		resourceErr error
		snapshot    *ExecutionSessionSnapshot
		wantRun     bool
		wantText    string
		wantError   string
	}{
		{"no gcode", false, nil, nil, false, "This step has no G-code to run.", ""},
		{"resource", true, errors.New("missing Code/a.nc"), nil, false, "Step G-code is unavailable.", "missing Code/a.nc"},
		{"invalid", true, nil, &invalid, false, "Step G-code is invalid.", "line 2: unsupported command"},
		{"rejected but eligible", true, nil, &rejected, true, "Previous Start rejected. This step can now be retried.", "controller is not ready"},
		{"disconnected", true, nil, &disconnected, false, "Machine is not connected.", ""},
		{"estop", true, nil, &estop, false, "Machine emergency stop is active.", ""},
		{"recovery", true, nil, &recovery, false, "Machine recovery is required before running.", ""},
		{"pending", true, nil, &pending, false, "A program Start admission is pending.", ""},
		{"rejected while blocked", true, nil, &rejectedDisconnected, false, "Machine is not connected.", "controller was not ready"},
		{"another active operation", true, nil, &active, false, "Another program is active.", ""},
		{"ready", true, nil, &ready, true, "Ready to run this step.", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PackageStepExecutionFeedback(tt.hasGCode, tt.resourceErr, tt.snapshot)
			if got.RunEnabled != tt.wantRun || got.Message != tt.wantText || got.Error != tt.wantError {
				t.Fatalf("feedback = %+v, want run=%v message=%q error=%q", got, tt.wantRun, tt.wantText, tt.wantError)
			}
		})
	}
}

func TestPackageStepFeedbackUsesTheSameCurrentRunPolicyAsAuxiliary(t *testing.T) {
	states := []struct {
		name  string
		state app.State
	}{
		{"ready", app.State{ConnectionStatus: app.ConnectionConnected, EStopStatus: app.EStopClear, MachineReadiness: app.MachineReadinessNormal}},
		{"disconnected", app.State{ConnectionStatus: app.ConnectionDisconnected, EStopStatus: app.EStopClear, MachineReadiness: app.MachineReadinessNormal}},
		{"recovery", app.State{ConnectionStatus: app.ConnectionConnected, EStopStatus: app.EStopClear, MachineReadiness: app.MachineReadinessRecoveryRequired}},
		{"active", app.State{ConnectionStatus: app.ConnectionConnected, EStopStatus: app.EStopClear, MachineReadiness: app.MachineReadinessNormal, ProgramDocumentID: 99, ProgramRunID: 4, ProgramStatus: app.ProgramRunning}},
	}
	for _, tt := range states {
		t.Run(tt.name, func(t *testing.T) {
			controls := SessionExecutionControlState(PackageStepExecutionSessionID(7, 1, "step"), 0, true, false, false, false, false, tt.state)
			snapshot := ExecutionSessionSnapshot{State: tt.state, Controls: controls, StartErr: errors.New("earlier rejection")}
			feedback := PackageStepExecutionFeedback(true, nil, &snapshot)
			if feedback.RunEnabled != controls.Run {
				t.Fatalf("package Run=%v, shared auxiliary policy Run=%v: %+v", feedback.RunEnabled, controls.Run, feedback)
			}
		})
	}
}

func TestPackageExecutionDisplayKeepsPendingAttemptSeparateFromHistory(t *testing.T) {
	completed := DocumentExecutionPresentation{
		RunID: 31, Status: app.ProgramCompleted, Total: 12, Complete: 12,
		Message: "Completed after controller Idle", Error: "old admission error",
	}
	pending := PackageExecutionPresentation{
		Presentation: completed, Phase: PackageExecutionStartPending,
		Controls: DocumentExecutionControls{Pause: true, Resume: true, Stop: true},
	}
	display := PackageExecutionDisplayFor(pending)
	if display.Prefix != "Start pending" || display.Error != "" || display.Status == "" || display.Progress != "Previous execution: Acknowledged commands: 12 / 12" {
		t.Fatalf("pending display inherited historical feedback: %+v", display)
	}
	if display.PauseEnabled || display.ResumeEnabled || display.StopEnabled {
		t.Fatalf("unadmitted attempt exposed actions: %+v", display)
	}

	rejected := pending
	rejected.Phase = PackageExecutionStartRejected
	rejected.AttemptErr = errors.New("second rejection")
	display = PackageExecutionDisplayFor(rejected)
	if display.Error != "second rejection" || display.Progress != "Acknowledged commands: 0 / 0" {
		t.Fatalf("rejected attempt did not use its own feedback: %+v", display)
	}

	awaiting := pending
	awaiting.Phase, awaiting.AttemptRunID = PackageExecutionAwaitingState, 32
	display = PackageExecutionDisplayFor(awaiting)
	if display.Error != "" || display.Status != "Start admitted as RunID 32 — awaiting controller state." || display.Progress != "Acknowledged commands: 0 / 0" {
		t.Fatalf("awaiting display inherited history: %+v", display)
	}
}

func TestExecutionAttemptDisplaySeparatesCurrentStartAttemptFromHistory(t *testing.T) {
	history := DocumentExecutionPresentation{
		DocumentID: 7, RunID: 31, Status: app.ProgramFailed, Total: 2, Complete: 2,
		Message: "Failed", Error: "run-scoped failure",
	}
	rejected := ExecutionSessionSnapshot{
		ID:           PackageStepExecutionSessionID(7, 1, "step"),
		Presentation: history,
		StartErr:     errors.New("admission rejected"),
		Controls:     DocumentExecutionControls{Run: true},
	}
	display := ExecutionAttemptDisplayFor(rejected)
	if display.Status != "Start rejected." || display.Error != "admission rejected" || display.Progress != "Acknowledged commands: 0 / 0" || !display.RunEnabled || display.PauseEnabled || display.ResumeEnabled || display.StopEnabled {
		t.Fatalf("rejected attempt display = %+v", display)
	}

	pending := rejected
	pending.StartErr = nil
	pending.StartPending = true
	pending.Controls = DocumentExecutionControls{}
	display = ExecutionAttemptDisplayFor(pending)
	if display.Status != "Start admission is pending." || display.Error != "" || display.Progress != "Acknowledged commands: 0 / 0" || display.RunEnabled || display.PauseEnabled || display.ResumeEnabled || display.StopEnabled {
		t.Fatalf("pending retry inherited historical feedback: %+v", display)
	}

	awaiting := pending
	awaiting.StartPending, awaiting.AwaitingRunState = false, true
	awaiting.Presentation.RunID = 32
	display = ExecutionAttemptDisplayFor(awaiting)
	if display.Status != "Start admitted as RunID 32 — awaiting controller state." || display.Error != "" || display.Progress != "Acknowledged commands: 0 / 0" {
		t.Fatalf("awaiting retry display = %+v", display)
	}

	standalone := ExecutionSessionSnapshot{
		ID:           StandaloneExecutionSessionID(8),
		Presentation: DocumentExecutionPresentation{DocumentID: 8, RunID: 9, Status: app.ProgramRunning, Total: 2, Complete: 1, NextLine: 5, Message: "Running"},
		Controls:     DocumentExecutionControls{Pause: true, Stop: true},
	}
	display = ExecutionAttemptDisplayFor(standalone)
	if display.Status != "Running" || display.Progress != "Acknowledged commands: 1 / 2" || display.NextLine != 5 || !display.PauseEnabled || !display.StopEnabled {
		t.Fatalf("standalone active display regressed: %+v", display)
	}
}
