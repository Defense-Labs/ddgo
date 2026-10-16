package frontend

import (
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
)

func TestMainWindowControlsMachineSafetyPolicy(t *testing.T) {
	available := MainWindowControlState{
		RefreshPortsEnabled:  true,
		ConnectEnabled:       true,
		PortSelectionEnabled: true,
	}
	manual := available
	manual.ManualControlsEnabled = true
	manual.UnlockEnabled = true
	manual.ResetEnabled = true
	jogToEnd := manual
	jogToEnd.JogToEndEnabled = true

	tests := []struct {
		name  string
		state app.State
		want  MainWindowControlState
	}{
		{
			name:  "disconnected",
			state: app.State{ConnectionStatus: app.ConnectionDisconnected, EStopStatus: app.EStopClear},
			want:  available,
		},
		{
			name:  "connecting",
			state: app.State{ConnectionStatus: app.ConnectionConnecting, EStopStatus: app.EStopClear},
			want: MainWindowControlState{
				RefreshPortsEnabled:  true,
				PortSelectionEnabled: true,
			},
		},
		{
			name: "connected idle without known position",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
			},
			want: manual,
		},
		{
			name: "connected idle with known position",
			state: app.State{
				ConnectionStatus:   app.ConnectionConnected,
				EStopStatus:        app.EStopClear,
				HasMachinePosition: true,
			},
			want: jogToEnd,
		},
		{
			name: "running document execution",
			state: app.State{
				ConnectionStatus:  app.ConnectionConnected,
				EStopStatus:       app.EStopClear,
				ProgramStatus:     app.ProgramRunning,
				ProgramDocumentID: 9,
				ProgramRunID:      12,
			},
			want: MainWindowControlState{},
		},
		{
			name: "paused legacy execution",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramPaused,
				ProgramRunID:     12,
			},
			want: MainWindowControlState{},
		},
		{
			name: "draining execution",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramDraining,
				ProgramRunID:     12,
			},
			want: MainWindowControlState{},
		},
		{
			name: "stopping execution",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramStopping,
				ProgramRunID:     12,
				MachineReadiness: app.MachineReadinessStopping,
			},
			want: MainWindowControlState{},
		},
		{
			name: "machine recovery required",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramFailed,
				MachineReadiness: app.MachineReadinessRecoveryRequired,
			},
			want: MainWindowControlState{
				RefreshPortsEnabled:  true,
				ConnectEnabled:       true,
				PortSelectionEnabled: true,
				ResetEnabled:         true,
			},
		},
		{
			name: "ordinary alarm recovery",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				MachineState:     "Alarm",
				ProgramStatus:    app.ProgramFailed,
				MachineReadiness: app.MachineReadinessRecoveryRequired,
			},
			want: MainWindowControlState{
				RefreshPortsEnabled:  true,
				ConnectEnabled:       true,
				PortSelectionEnabled: true,
				UnlockEnabled:        true,
				ResetEnabled:         true,
			},
		},
		{
			name: "E-stop active",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopActive,
			},
			want: available,
		},
		{
			name: "E-stop recovery",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopRecovery,
			},
			want: MainWindowControlState{
				RefreshPortsEnabled:  true,
				ConnectEnabled:       true,
				PortSelectionEnabled: true,
				UnlockEnabled:        true,
				ResetEnabled:         true,
			},
		},
		{
			name: "recovery while disconnected",
			state: app.State{
				ConnectionStatus: app.ConnectionDisconnected,
				EStopStatus:      app.EStopRecovery,
				MachineReadiness: app.MachineReadinessRecoveryRequired,
			},
			want: available,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MainWindowControls(tt.state); got != tt.want {
				t.Fatalf("MainWindowControls() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMainWindowControlsExecutionOwnershipDoesNotChangeMachinePolicy(t *testing.T) {
	base := app.State{
		ConnectionStatus: app.ConnectionConnected,
		EStopStatus:      app.EStopClear,
		ProgramStatus:    app.ProgramRunning,
		ProgramRunID:     12,
	}
	legacy := MainWindowControls(base)
	document := base
	document.ProgramDocumentID = 9
	if got := MainWindowControls(document); got != legacy {
		t.Fatalf("document execution controls = %+v, legacy execution controls = %+v", got, legacy)
	}
}
