package frontend

import (
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
)

func TestMainWindowControls(t *testing.T) {
	available := MainWindowControlState{
		RefreshPortsEnabled:  true,
		ConnectEnabled:       true,
		PortSelectionEnabled: true,
		ProgramPathEnabled:   true,
		BrowseProgramEnabled: true,
	}
	manual := available
	manual.ManualControlsEnabled = true
	manual.UnlockEnabled = true
	manual.ResetEnabled = true
	loaded := manual
	loaded.RunProgramEnabled = true
	jogToEnd := loaded
	jogToEnd.JogToEndEnabled = true

	tests := []struct {
		name  string
		state app.State
		want  MainWindowControlState
	}{
		{
			name: "disconnected no program",
			state: app.State{
				ConnectionStatus: app.ConnectionDisconnected,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramNotLoaded,
			},
			want: available,
		},
		{
			name: "connecting",
			state: app.State{
				ConnectionStatus: app.ConnectionConnecting,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramNotLoaded,
			},
			want: MainWindowControlState{
				RefreshPortsEnabled:  true,
				PortSelectionEnabled: true,
				ProgramPathEnabled:   true,
				BrowseProgramEnabled: true,
			},
		},
		{
			name: "connected idle no program",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramNotLoaded,
			},
			want: manual,
		},
		{
			name: "connected idle loaded program",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramLoaded,
				ProgramTotal:     10,
			},
			want: loaded,
		},
		{
			name: "running program",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramRunning,
				ProgramTotal:     10,
			},
			want: MainWindowControlState{
				PauseProgramEnabled: true,
				StopProgramEnabled:  true,
			},
		},
		{
			name: "paused program",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramPaused,
				ProgramTotal:     10,
			},
			want: MainWindowControlState{
				ResumeProgramEnabled: true,
				StopProgramEnabled:   true,
			},
		},
		{
			name: "E-stop active",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopActive,
				ProgramStatus:    app.ProgramLoaded,
				ProgramTotal:     10,
			},
			want: available,
		},
		{
			name: "E-stop recovery",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopRecovery,
				ProgramStatus:    app.ProgramLoaded,
				ProgramTotal:     10,
			},
			want: MainWindowControlState{
				RefreshPortsEnabled:  true,
				ConnectEnabled:       true,
				PortSelectionEnabled: true,
				ProgramPathEnabled:   true,
				BrowseProgramEnabled: true,
				UnlockEnabled:        true,
				ResetEnabled:         true,
			},
		},
		{
			name: "recovery while disconnected",
			state: app.State{
				ConnectionStatus: app.ConnectionDisconnected,
				EStopStatus:      app.EStopRecovery,
				ProgramStatus:    app.ProgramNotLoaded,
			},
			want: available,
		},
		{
			name: "jog to end without machine position",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramLoaded,
				ProgramTotal:     10,
			},
			want: loaded,
		},
		{
			name: "jog to end with machine position",
			state: app.State{
				ConnectionStatus:   app.ConnectionConnected,
				EStopStatus:        app.EStopClear,
				ProgramStatus:      app.ProgramLoaded,
				ProgramTotal:       10,
				HasMachinePosition: true,
			},
			want: jogToEnd,
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
