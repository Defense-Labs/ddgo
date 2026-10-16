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
