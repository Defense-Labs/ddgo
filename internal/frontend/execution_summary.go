package frontend

import (
	"fmt"

	"github.com/ianbruene/ddgo/internal/app"
)

// ExecutionSummary is the toolkit-neutral, read-only presentation of the
// controller's current active run or most recent completed run. It deliberately
// contains no execution commands: document windows own Run/Pause/Resume/Stop.
type ExecutionSummary struct {
	Heading  string
	Name     string
	Status   string
	Progress string
	Detail   string

	Complete int
	Total    int

	DocumentID app.DocumentID
	RunID      app.RunID
}

// ExecutionSummaryForState derives the global execution presentation solely
// from controller state. A loaded legacy program has no RunID and is therefore
// not presented as an execution.
func ExecutionSummaryForState(state app.State) ExecutionSummary {
	summary := ExecutionSummary{
		Heading:  "Current Execution",
		Name:     "No program running",
		Status:   "No execution yet",
		Progress: "No commands acknowledged",
	}
	if state.ProgramRunID == 0 {
		return summary
	}

	summary.DocumentID = state.ProgramDocumentID
	summary.RunID = state.ProgramRunID
	summary.Name = state.ProgramName
	if summary.Name == "" {
		summary.Name = "Unnamed program"
	}
	summary.Complete = state.ProgramComplete
	summary.Total = state.ProgramTotal
	summary.Progress = fmt.Sprintf("%d / %d commands acknowledged", state.ProgramComplete, state.ProgramTotal)
	summary.Status = executionStatusText(state.ProgramStatus)
	if !state.ProgramStatus.IsActive() {
		summary.Heading = "Last Execution"
	}
	switch state.ProgramStatus {
	case app.ProgramDraining:
		summary.Detail = "All source commands may be acknowledged; waiting for controller Idle synchronization."
	case app.ProgramStopping:
		summary.Detail = "Stopping and verifying machine readiness."
	case app.ProgramFailed:
		summary.Detail = state.ProgramError
	case app.ProgramStopped:
		summary.Detail = "Stopped after verified controller recovery."
	case app.ProgramCompleted:
		summary.Detail = "Completed after controller Idle synchronization."
	}
	return summary
}

func executionStatusText(status app.ProgramStatus) string {
	switch status {
	case app.ProgramRunning:
		return "Running"
	case app.ProgramPaused:
		return "Paused"
	case app.ProgramDraining:
		return "Finishing — waiting for machine Idle"
	case app.ProgramStopping:
		return "Stopping"
	case app.ProgramStopped:
		return "Stopped"
	case app.ProgramCompleted:
		return "Completed"
	case app.ProgramFailed:
		return "Failed"
	default:
		return "No execution yet"
	}
}

// CanFocusExecutionSummary requires the exact run identity still to be the
// controller's presented run and its original document identity to remain
// registered. A reopened file has a new DocumentID and cannot satisfy this.
func CanFocusExecutionSummary(state app.State, documentID app.DocumentID, runID app.RunID, documentOpen bool) bool {
	return documentOpen && documentID != 0 && runID != 0 &&
		state.ProgramDocumentID == documentID && state.ProgramRunID == runID
}
