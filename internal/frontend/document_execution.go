package frontend

import (
	"fmt"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/gcode"
)

// DocumentExecutionControls is the toolkit-neutral policy for one document's
// controls. The controller remains the authority for every admitted action.
type DocumentExecutionControls struct {
	Run    bool
	Pause  bool
	Resume bool
	Stop   bool
}

// PackageStepFeedback is the compact, toolkit-independent explanation shown
// beside Run Selected Step. It is derived from the session snapshot and
// controller state rather than from Qt selection state.
type PackageStepFeedback struct {
	RunEnabled bool
	Message    string
	Error      string
}

// ExecutionAttemptDisplay is the toolkit-neutral execution panel view for a
// single session. It intentionally separates a current Start attempt from the
// retained admitted-run presentation so every view agrees about which error
// and progress belong to the current attempt.
type ExecutionAttemptDisplay struct {
	Status          string
	Progress        string
	ProgressMaximum int
	ProgressValue   int
	Error           string
	NextLine        int
	Draining        bool
	RunEnabled      bool
	PauseEnabled    bool
	ResumeEnabled   bool
	StopEnabled     bool
}

// ExecutionAttemptDisplayFor is shared by standalone and package auxiliary
// G-code windows. A pending or awaiting Start has not reached authoritative
// controller execution state, so it cannot inherit prior errors, progress, or
// active controls from the retained presentation.
func ExecutionAttemptDisplayFor(snapshot ExecutionSessionSnapshot) ExecutionAttemptDisplay {
	controls := snapshot.Controls
	if snapshot.StartPending {
		return ExecutionAttemptDisplay{
			Status: "Start admission is pending.", Progress: "Acknowledged commands: 0 / 0",
			ProgressMaximum: 1,
		}
	}
	if snapshot.AwaitingRunState {
		status := "Start admitted — awaiting controller state."
		if snapshot.Presentation.RunID != 0 {
			status = fmt.Sprintf("Start admitted as RunID %d — awaiting controller state.", snapshot.Presentation.RunID)
		}
		return ExecutionAttemptDisplay{
			Status: status, Progress: "Acknowledged commands: 0 / 0", ProgressMaximum: 1,
		}
	}
	if snapshot.StartErr != nil {
		return ExecutionAttemptDisplay{
			Status: "Start rejected.", Progress: "Acknowledged commands: 0 / 0",
			ProgressMaximum: 1, Error: snapshot.StartErr.Error(), RunEnabled: controls.Run,
		}
	}
	presentation := snapshot.Presentation
	maximum := presentation.Total
	if maximum < 1 {
		maximum = 1
	}
	return ExecutionAttemptDisplay{
		Status:          presentation.StatusText(snapshot.State),
		Progress:        fmt.Sprintf("Acknowledged commands: %d / %d", presentation.Complete, presentation.Total),
		ProgressMaximum: maximum, ProgressValue: presentation.Complete, Error: presentation.Error,
		NextLine: presentation.NextLine, Draining: presentation.Status == app.ProgramDraining,
		RunEnabled: controls.Run, PauseEnabled: controls.Pause, ResumeEnabled: controls.Resume, StopEnabled: controls.Stop,
	}
}

// PackageExecutionDisplay is the toolkit-neutral presentation of the package
// execution strip. Attempt phases deliberately do not borrow error or progress
// text from a retained admitted run: a new admission is not that prior run.
type PackageExecutionDisplay struct {
	Prefix          string
	Status          string
	Progress        string
	ProgressMaximum int
	ProgressValue   int
	Error           string
	PauseEnabled    bool
	ResumeEnabled   bool
	StopEnabled     bool
}

// PackageExecutionDisplayFor keeps the package strip's attempt feedback
// separate from retained RunID presentation. It is shared by the Qt window
// and toolkit-independent tests so a pending retry cannot show an old Start
// rejection or a historical run error as its own failure.
func PackageExecutionDisplayFor(snapshot PackageExecutionPresentation) PackageExecutionDisplay {
	presentation := snapshot.Presentation
	if snapshot.Phase == PackageExecutionStartRejected {
		status := "No program was started."
		if presentation.RunID != 0 {
			status += fmt.Sprintf(" Previous admitted RunID %d remains in this step's history.", presentation.RunID)
		}
		errorText := ""
		if snapshot.AttemptErr != nil {
			errorText = snapshot.AttemptErr.Error()
		}
		return PackageExecutionDisplay{
			Prefix: "Start rejected", Status: status, Progress: "Acknowledged commands: 0 / 0",
			ProgressMaximum: 1, Error: errorText,
		}
	}
	if snapshot.Phase == PackageExecutionStartPending {
		progress := "Acknowledged commands: 0 / 0"
		status := "Start admission is pending."
		if presentation.RunID != 0 {
			status += fmt.Sprintf(" Previous admitted RunID %d remains in this step's history.", presentation.RunID)
			progress = fmt.Sprintf("Previous execution: Acknowledged commands: %d / %d", presentation.Complete, presentation.Total)
		}
		return PackageExecutionDisplay{Prefix: "Start pending", Status: status, Progress: progress, ProgressMaximum: 1}
	}
	if snapshot.Phase == PackageExecutionAwaitingState {
		runText := "Start admitted — awaiting controller state."
		if snapshot.AttemptRunID != 0 {
			runText = fmt.Sprintf("Start admitted as RunID %d — awaiting controller state.", snapshot.AttemptRunID)
		}
		return PackageExecutionDisplay{
			Prefix: "Start admitted — awaiting controller state", Status: runText,
			Progress: "Acknowledged commands: 0 / 0", ProgressMaximum: 1,
		}
	}
	prefix := "Last execution"
	if snapshot.Phase == PackageExecutionActive {
		prefix = "Current execution"
	}
	maximum := presentation.Total
	if maximum < 1 {
		maximum = 1
	}
	return PackageExecutionDisplay{
		Prefix: prefix, Status: "Status: " + presentation.StatusText(snapshot.State),
		Progress:        fmt.Sprintf("Acknowledged commands: %d / %d", presentation.Complete, presentation.Total),
		ProgressMaximum: maximum, ProgressValue: presentation.Complete, Error: presentation.Error,
		PauseEnabled: snapshot.Controls.Pause, ResumeEnabled: snapshot.Controls.Resume, StopEnabled: snapshot.Controls.Stop,
	}
}

func PackageStepExecutionFeedback(hasGCode bool, resourceErr error, snapshot *ExecutionSessionSnapshot) PackageStepFeedback {
	if !hasGCode {
		return PackageStepFeedback{Message: "This step has no G-code to run."}
	}
	if resourceErr != nil {
		return PackageStepFeedback{Message: "Step G-code is unavailable.", Error: resourceErr.Error()}
	}
	if snapshot == nil {
		return PackageStepFeedback{Message: "Preparing step execution…"}
	}
	if snapshot.ParseError != nil {
		return PackageStepFeedback{Message: "Step G-code is invalid.", Error: snapshot.ParseError.Error()}
	}
	if snapshot.Controls.Run {
		if snapshot.StartErr != nil {
			return PackageStepFeedback{
				RunEnabled: true,
				Message:    "Previous Start rejected. This step can now be retried.",
				Error:      snapshot.StartErr.Error(),
			}
		}
		return PackageStepFeedback{RunEnabled: true, Message: "Ready to run this step."}
	}
	// StartErr describes a completed historical admission attempt. It can be
	// useful feedback, but it is never the authority for current eligibility:
	// the session control policy below remains authoritative.
	feedback := PackageStepFeedback{}
	state := snapshot.State
	switch {
	case !state.IsConnected():
		feedback.Message = "Machine is not connected."
	case state.EStopStatus != app.EStopClear:
		feedback.Message = "Machine emergency stop is active."
	case state.MachineReadiness != app.MachineReadinessNormal:
		feedback.Message = "Machine recovery is required before running."
	case snapshot.StartPending || snapshot.AwaitingRunState || snapshot.OwnerStartBlocked:
		feedback.Message = "A program Start admission is pending."
	case state.ProgramStatus.IsActive():
		feedback.Message = "Another program is active."
	default:
		feedback.Message = "This step is not currently eligible to run."
	}
	if snapshot.StartErr != nil {
		feedback.Error = snapshot.StartErr.Error()
	}
	return feedback
}

// SourceHighlightAction separates logical execution highlighting from native
// widget behavior. Reveal is requested only when a new line is highlighted or
// Follow Execution was just enabled; repeated state repainting is inert.
type SourceHighlightAction struct {
	Apply  bool
	Reveal bool
}

func SourceHighlightUpdate(currentLine, nextLine int, follow, wasFollowing bool) SourceHighlightAction {
	changed := currentLine != nextLine
	followEnabled := follow && !wasFollowing
	return SourceHighlightAction{
		Apply:  changed || followEnabled,
		Reveal: nextLine > 0 && follow && (changed || followEnabled),
	}
}

func DocumentExecutionControlState(documentID app.DocumentID, executable bool, startPending, operationPending bool, state app.State) DocumentExecutionControls {
	owner := state.ProgramDocumentID == documentID && state.ProgramRunID != 0
	active := state.ProgramStatus.IsActive()
	ready := state.IsConnected() && state.EStopStatus == app.EStopClear && state.MachineReadiness == app.MachineReadinessNormal
	return DocumentExecutionControls{
		Run:    executable && !startPending && !operationPending && !active && ready,
		Pause:  owner && !operationPending && (state.ProgramStatus == app.ProgramRunning || state.ProgramStatus == app.ProgramDraining),
		Resume: owner && !operationPending && state.ProgramStatus == app.ProgramPaused && ready,
		Stop:   owner && !operationPending && active && state.ProgramStatus != app.ProgramStopping,
	}
}

// DocumentExecutionPresentation retains the most recent run belonging to one
// document even while another document or legacy program owns the machine.
type DocumentExecutionPresentation struct {
	DocumentID app.DocumentID
	RunID      app.RunID
	Status     app.ProgramStatus
	Total      int
	Complete   int
	NextLine   int // physical source line; zero when no executable line remains
	Message    string
	Error      string
	lines      []gcode.Line
}

func NewDocumentExecutionPresentation(documentID app.DocumentID, program gcode.Program) DocumentExecutionPresentation {
	lines := append([]gcode.Line(nil), program.Lines...)
	return DocumentExecutionPresentation{DocumentID: documentID, Status: app.ProgramNotLoaded, lines: lines, Total: len(lines)}
}

func (p *DocumentExecutionPresentation) SetProgram(program gcode.Program) {
	p.lines = append(p.lines[:0], program.Lines...)
	p.Total = len(p.lines)
}

func (p *DocumentExecutionPresentation) ApplyState(state app.State) {
	if state.ProgramDocumentID != p.DocumentID || state.ProgramRunID == 0 {
		return
	}
	if p.RunID != 0 && state.ProgramRunID < p.RunID {
		return
	}
	p.RunID = state.ProgramRunID
	p.Status = state.ProgramStatus
	p.Total = state.ProgramTotal
	p.Complete = state.ProgramComplete
	p.Error = state.ProgramError
	p.NextLine = 0
	if (p.Status == app.ProgramRunning || p.Status == app.ProgramPaused) && p.Complete >= 0 && p.Complete < len(p.lines) {
		p.NextLine = p.lines[p.Complete].Number
	}
	switch p.Status {
	case app.ProgramDraining:
		p.Message = "All commands acknowledged — awaiting controller Idle"
	case app.ProgramCompleted:
		p.Message = "Completed after controller Idle"
	case app.ProgramFailed:
		p.Message = "Failed"
	case app.ProgramStopping:
		p.Message = "Stopping — verifying controller recovery"
	case app.ProgramStopped:
		p.Message = "Stopped"
	case app.ProgramPaused:
		p.Message = "Paused"
	case app.ProgramRunning:
		p.Message = "Running"
	}
}

func (p *DocumentExecutionPresentation) ApplyEvent(event app.Event) {
	if event.Kind != app.EventError || event.ErrorDocumentID != p.DocumentID || event.ErrorRunID == 0 || event.ErrorRunID != p.RunID {
		return
	}
	if event.Err != nil {
		p.Error = event.Err.Error()
	} else if event.Text != "" {
		p.Error = event.Text
	}
}

// ReconcileAdmission records the result of this document's asynchronous Run
// request. A successful admission only confirms a RunID: controller state may
// already have advanced to a terminal result by the time the UI drains this
// result, so it must not clear a same-run failure or replace a newer run.
func (p *DocumentExecutionPresentation) ReconcileAdmission(runID app.RunID, err error) {
	if err != nil {
		p.Error = err.Error()
		p.Message = "Run was not admitted"
		return
	}
	if runID == 0 || p.RunID > runID {
		return
	}
	if p.RunID == runID {
		return
	}
	// This is a genuinely newer run for this document. State events may still
	// arrive later, but the old run's local terminal error cannot carry over.
	p.RunID = runID
	p.Status = app.ProgramRunning
	p.Total = len(p.lines)
	p.Complete = 0
	p.Error = ""
	p.Message = "Running"
	p.NextLine = 0
	if len(p.lines) != 0 {
		p.NextLine = p.lines[0].Number
	}
}

func (p DocumentExecutionPresentation) StatusText(current app.State) string {
	if p.RunID == 0 {
		if current.ProgramStatus.IsActive() {
			return "Not run — machine occupied"
		}
		return "Not run"
	}
	if p.Message != "" {
		return p.Message
	}
	return fmt.Sprintf("%s", p.Status)
}
