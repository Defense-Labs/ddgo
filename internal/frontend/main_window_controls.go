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
	return MainWindowControlsWithLegacyOperations(state, false, false)
}

// MainWindowControlsWithLegacyOperations adds the frontend-only coordination
// state for the legacy Load/Run workflow. Controller state remains the
// authority for all machine and execution admission checks.
func MainWindowControlsWithLegacyOperations(state app.State, legacyLoadPending, legacyRunPending bool) MainWindowControlState {
	programActive := state.ProgramStatus.IsActive()
	connected := state.IsConnected()
	safetyClear := state.EStopStatus == app.EStopClear
	machineReady := state.MachineReadiness == app.MachineReadinessNormal
	machineRecoveryRequired := state.MachineReadiness == app.MachineReadinessRecoveryRequired
	recovering := state.EStopStatus == app.EStopRecovery
	ordinaryAlarmRecovery := machineRecoveryRequired && strings.EqualFold(state.MachineState, "Alarm")
	loaded := state.LegacyProgramLoaded && state.LegacyProgramTotal > 0
	legacyRun := state.ProgramDocumentID == 0 && state.ProgramRunID != 0

	canManual := connected && safetyClear && machineReady && !programActive
	legacyOperationPending := legacyLoadPending || legacyRunPending
	canRun := connected && safetyClear && machineReady && loaded && !programActive && !legacyOperationPending

	return MainWindowControlState{
		RefreshPortsEnabled:  !programActive,
		ConnectEnabled:       !programActive && !state.IsConnecting(),
		PortSelectionEnabled: !programActive,

		ProgramPathEnabled:   !programActive && !legacyOperationPending,
		BrowseProgramEnabled: !programActive && !legacyOperationPending,
		RunProgramEnabled:    canRun,
		PauseProgramEnabled:  legacyRun && (state.ProgramStatus == app.ProgramRunning || state.ProgramStatus == app.ProgramDraining),
		ResumeProgramEnabled: legacyRun && state.ProgramStatus == app.ProgramPaused && machineReady,
		StopProgramEnabled:   legacyRun && programActive && state.ProgramStatus != app.ProgramStopping,

		ManualControlsEnabled: canManual,
		UnlockEnabled: canManual ||
			(connected && !programActive && (recovering || ordinaryAlarmRecovery)),
		ResetEnabled: canManual ||
			(connected && (recovering || machineRecoveryRequired) && !programActive),
		JogToEndEnabled: canManual && state.HasMachinePosition,
	}
}
