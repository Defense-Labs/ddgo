package frontend

import "github.com/ianbruene/ddgo/internal/app"

type MainWindowControlState struct {
	RefreshPortsEnabled  bool
	ConnectEnabled       bool
	PortSelectionEnabled bool

	ProgramPathEnabled   bool
	BrowseProgramEnabled bool
	RunProgramEnabled    bool
	PauseProgramEnabled  bool
	ResumeProgramEnabled bool
	StopProgramEnabled   bool

	ManualControlsEnabled bool
	UnlockEnabled         bool
	ResetEnabled          bool
	JogToEndEnabled       bool
}

func MainWindowControls(state app.State) MainWindowControlState {
	programActive := state.ProgramStatus.IsActive()
	connected := state.IsConnected()
	safetyClear := state.EStopStatus == app.EStopClear
	recovering := state.EStopStatus == app.EStopRecovery
	loaded := state.ProgramTotal > 0

	canManual := connected && safetyClear && !programActive
	canRun := connected && safetyClear && loaded && !programActive

	return MainWindowControlState{
		RefreshPortsEnabled:  !programActive,
		ConnectEnabled:       !programActive && !state.IsConnecting(),
		PortSelectionEnabled: !programActive,

		ProgramPathEnabled:   !programActive,
		BrowseProgramEnabled: !programActive,
		RunProgramEnabled:    canRun,
		PauseProgramEnabled:  state.ProgramStatus == app.ProgramRunning,
		ResumeProgramEnabled: state.ProgramStatus == app.ProgramPaused,
		StopProgramEnabled:   programActive,

		ManualControlsEnabled: canManual,
		UnlockEnabled: canManual ||
			(connected && recovering && !programActive),
		ResetEnabled: canManual ||
			(connected && recovering && !programActive),
		JogToEndEnabled: canManual && state.HasMachinePosition,
	}
}
