package frontend

import (
	"strings"

	"github.com/ianbruene/ddgo/internal/app"
)

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
	machineReady := state.MachineReadiness == app.MachineReadinessNormal
	machineRecoveryRequired := state.MachineReadiness == app.MachineReadinessRecoveryRequired
	recovering := state.EStopStatus == app.EStopRecovery
	ordinaryAlarmRecovery := machineRecoveryRequired && strings.EqualFold(state.MachineState, "Alarm")
	loaded := state.ProgramTotal > 0

	canManual := connected && safetyClear && machineReady && !programActive
	canRun := connected && safetyClear && machineReady && loaded && !programActive

	return MainWindowControlState{
		RefreshPortsEnabled:  !programActive,
		ConnectEnabled:       !programActive && !state.IsConnecting(),
		PortSelectionEnabled: !programActive,

		ProgramPathEnabled:   !programActive,
		BrowseProgramEnabled: !programActive,
		RunProgramEnabled:    canRun,
		PauseProgramEnabled:  state.ProgramStatus == app.ProgramRunning || state.ProgramStatus == app.ProgramDraining,
		ResumeProgramEnabled: state.ProgramStatus == app.ProgramPaused && machineReady,
		StopProgramEnabled:   programActive && state.ProgramStatus != app.ProgramStopping,

		ManualControlsEnabled: canManual,
		UnlockEnabled: canManual ||
			(connected && !programActive && (recovering || ordinaryAlarmRecovery)),
		ResetEnabled: canManual ||
			(connected && (recovering || machineRecoveryRequired) && !programActive),
		JogToEndEnabled: canManual && state.HasMachinePosition,
	}
}
