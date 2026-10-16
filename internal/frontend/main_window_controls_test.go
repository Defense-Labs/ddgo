package frontend

import (
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
)

func TestMainWindowControlsPreserveMachineSafetyPolicy(t *testing.T) {
	ready := app.State{
		ConnectionStatus:   app.ConnectionConnected,
		EStopStatus:        app.EStopClear,
		MachineReadiness:   app.MachineReadinessNormal,
		HasMachinePosition: true,
	}
	controls := MainWindowControls(ready)
	if !controls.ManualControlsEnabled || !controls.UnlockEnabled || !controls.ResetEnabled || !controls.JogToEndEnabled {
		t.Fatalf("ready controls = %+v", controls)
	}

	active := ready
	active.ProgramStatus = app.ProgramRunning
	controls = MainWindowControls(active)
	if controls.ManualControlsEnabled || controls.JogToEndEnabled || controls.ConnectEnabled {
		t.Fatalf("active execution loosened machine admission: %+v", controls)
	}

	recovery := ready
	recovery.MachineReadiness = app.MachineReadinessRecoveryRequired
	recovery.MachineState = "Alarm"
	controls = MainWindowControls(recovery)
	if controls.ManualControlsEnabled || !controls.UnlockEnabled || !controls.ResetEnabled {
		t.Fatalf("ordinary alarm recovery controls = %+v", controls)
	}
}

func TestMainWindowControlsContainOnlyMachinePolicies(t *testing.T) {
	controls := MainWindowControls(app.State{
		ConnectionStatus:    app.ConnectionConnected,
		EStopStatus:         app.EStopClear,
		MachineReadiness:    app.MachineReadinessNormal,
		ProgramStatus:       app.ProgramRunning,
		ProgramDocumentID:   9,
		ProgramRunID:        12,
		LegacyProgramLoaded: true,
		LegacyProgramTotal:  3,
	})
	if controls.ManualControlsEnabled || controls.ConnectEnabled || controls.PortSelectionEnabled {
		t.Fatalf("active execution machine policy = %+v", controls)
	}
}
