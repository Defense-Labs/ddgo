package frontend

import (
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
)

func TestFormatProgramStatus(t *testing.T) {
	tests := []struct {
		name        string
		status      app.ProgramStatus
		programName string
		want        string
	}{
		{name: "not loaded", status: app.ProgramNotLoaded, programName: "example.nc", want: "not loaded"},
		{name: "loaded", status: app.ProgramLoaded, programName: "example.nc", want: "loaded (example.nc)"},
		{name: "running", status: app.ProgramRunning, programName: "example.nc", want: "running (example.nc)"},
		{name: "paused", status: app.ProgramPaused, programName: "example.nc", want: "paused (example.nc)"},
		{name: "stopped", status: app.ProgramStopped, programName: "example.nc", want: "stopped (example.nc)"},
		{name: "completed", status: app.ProgramCompleted, programName: "example.nc", want: "completed (example.nc)"},
		{name: "failed", status: app.ProgramFailed, programName: "example.nc", want: "failed (example.nc)"},
		{name: "unknown", status: app.ProgramStatus("custom"), programName: "example.nc", want: "custom"},
		{name: "empty name", status: app.ProgramLoaded, want: "loaded ()"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := app.State{ProgramStatus: tt.status, ProgramName: tt.programName}
			if got := FormatProgramStatus(state); got != tt.want {
				t.Fatalf("FormatProgramStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}
