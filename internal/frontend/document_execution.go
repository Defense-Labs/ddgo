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
	if p.Complete >= 0 && p.Complete < len(p.lines) {
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
