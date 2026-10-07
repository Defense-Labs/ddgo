package frontend

import (
	"fmt"

	"github.com/ianbruene/ddgo/internal/app"
)

func FormatProgramStatus(state app.State) string {
	switch state.ProgramStatus {
	case app.ProgramNotLoaded:
		return "not loaded"
	case app.ProgramLoaded:
		return fmt.Sprintf("loaded (%s)", state.ProgramName)
	case app.ProgramRunning:
		return fmt.Sprintf("running (%s)", state.ProgramName)
	case app.ProgramPaused:
		return fmt.Sprintf("paused (%s)", state.ProgramName)
	case app.ProgramStopped:
		return fmt.Sprintf("stopped (%s)", state.ProgramName)
	case app.ProgramCompleted:
		return fmt.Sprintf("completed (%s)", state.ProgramName)
	case app.ProgramFailed:
		return fmt.Sprintf("failed (%s)", state.ProgramName)
	default:
		return string(state.ProgramStatus)
	}
}
