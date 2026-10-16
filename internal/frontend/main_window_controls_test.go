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
				ConnectionStatus:    app.ConnectionConnected,
				EStopStatus:         app.EStopClear,
				ProgramStatus:       app.ProgramLoaded,
				ProgramTotal:        10,
				LegacyProgramLoaded: true,
				LegacyProgramTotal:  10,
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
				ProgramRunID:     1,
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
				ProgramRunID:     1,
			},
			want: MainWindowControlState{
				ResumeProgramEnabled: true,
				StopProgramEnabled:   true,
			},
		},
		{
			name: "draining program",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramDraining,
				ProgramTotal:     10,
				ProgramComplete:  10,
				ProgramRunID:     1,
			},
			want: MainWindowControlState{
				PauseProgramEnabled: true,
				StopProgramEnabled:  true,
			},
		},
		{
			name: "document-owned program",
			state: app.State{
				ConnectionStatus:  app.ConnectionConnected,
				EStopStatus:       app.EStopClear,
				ProgramStatus:     app.ProgramRunning,
				ProgramTotal:      10,
				ProgramDocumentID: 9,
				ProgramRunID:      1,
			},
			want: MainWindowControlState{},
		},
		{
			name: "stopping program",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				ProgramStatus:    app.ProgramStopping,
				MachineReadiness: app.MachineReadinessStopping,
				ProgramTotal:     10,
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
				ProgramTotal:     10,
			},
			want: MainWindowControlState{
				RefreshPortsEnabled:  true,
				ConnectEnabled:       true,
				PortSelectionEnabled: true,
				ProgramPathEnabled:   true,
				BrowseProgramEnabled: true,
				ResetEnabled:         true,
			},
		},
		{
			name: "ordinary alarm recovery permits unlock only",
			state: app.State{
				ConnectionStatus: app.ConnectionConnected,
				EStopStatus:      app.EStopClear,
				MachineState:     "Alarm",
				ProgramStatus:    app.ProgramFailed,
				MachineReadiness: app.MachineReadinessRecoveryRequired,
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
				ConnectionStatus:    app.ConnectionConnected,
				EStopStatus:         app.EStopClear,
				ProgramStatus:       app.ProgramLoaded,
				ProgramTotal:        10,
				LegacyProgramLoaded: true,
				LegacyProgramTotal:  10,
			},
			want: loaded,
		},
		{
			name: "jog to end with machine position",
			state: app.State{
				ConnectionStatus:    app.ConnectionConnected,
				EStopStatus:         app.EStopClear,
				ProgramStatus:       app.ProgramLoaded,
				ProgramTotal:        10,
				HasMachinePosition:  true,
				LegacyProgramLoaded: true,
				LegacyProgramTotal:  10,
			},
			want: jogToEnd,
		},
	}

	// Current execution fields can describe a completed document, not a
	// program selected by the legacy main-window workflow.
	documentCompleted := MainWindowControls(app.State{
		ConnectionStatus:  app.ConnectionConnected,
		EStopStatus:       app.EStopClear,
		ProgramStatus:     app.ProgramCompleted,
		ProgramDocumentID: 8,
		ProgramRunID:      3,
		ProgramTotal:      10,
	})
	if documentCompleted.RunProgramEnabled {
		t.Fatal("document completion enabled legacy Run without a loaded legacy program")
	}
	legacyRetained := MainWindowControls(app.State{
		ConnectionStatus:    app.ConnectionConnected,
		EStopStatus:         app.EStopClear,
		ProgramStatus:       app.ProgramCompleted,
		ProgramDocumentID:   8,
		ProgramRunID:        3,
		ProgramTotal:        10,
		LegacyProgramLoaded: true,
		LegacyProgramTotal:  2,
		LegacyProgramPath:   "legacy.gcode",
		LegacyProgramName:   "legacy.gcode",
	})
	if !legacyRetained.RunProgramEnabled {
		t.Fatal("retained legacy program was not runnable after document completion")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MainWindowControls(tt.state); got != tt.want {
				t.Fatalf("MainWindowControls() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
