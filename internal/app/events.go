package app

import (
	"time"

	"github.com/ianbruene/ddgo/internal/transport"
)

type EventKind string

type StateRevision uint64

// DocumentID identifies the application document that originated a program
// run. Zero is reserved for the legacy loaded-program workflow.
type DocumentID uint64

// RunID identifies one successfully admitted program execution. Run IDs are
// allocated by a Controller and are never reused during its lifetime.
type RunID uint64

type ProgramStatus string

// MachineReadiness is the controller's independent post-abort execution
// interlock. Its zero value is the normal/ready state.
type MachineReadiness string

type ConnectionStatus string

type EStopStatus string

type EStopSource string

type AvailablePort struct {
	Name string
}

const (
	EventStateChanged   EventKind = "state_changed"
	EventPortsRefreshed EventKind = "ports_refreshed"
	EventConsoleRX      EventKind = "console_rx"
	EventConsoleTX      EventKind = "console_tx"
	EventError          EventKind = "error"
)

const (
	ConnectionDisconnected ConnectionStatus = "disconnected"
	ConnectionConnecting   ConnectionStatus = "connecting"
	ConnectionConnected    ConnectionStatus = "connected"
)

const (
	EStopClear    EStopStatus = "clear"
	EStopActive   EStopStatus = "active"
	EStopRecovery EStopStatus = "recovery"
)

const (
	EStopSourceNone         EStopSource = ""
	EStopSourceAlarm50      EStopSource = "alarm50"
	EStopSourceUnresponsive EStopSource = "unresponsive"
)

const (
	ProgramNotLoaded ProgramStatus = "not_loaded"
	ProgramLoaded    ProgramStatus = "loaded"
	ProgramRunning   ProgramStatus = "running"
	ProgramDraining  ProgramStatus = "draining"
	ProgramPaused    ProgramStatus = "paused"
	ProgramStopping  ProgramStatus = "stopping"
	ProgramStopped   ProgramStatus = "stopped"
	ProgramCompleted ProgramStatus = "completed"
	ProgramFailed    ProgramStatus = "failed"
)

func (s ProgramStatus) IsActive() bool {
	switch s {
	case ProgramRunning, ProgramDraining, ProgramPaused, ProgramStopping:
		return true
	default:
		return false
	}
}

const (
	MachineReadinessNormal           MachineReadiness = ""
	MachineReadinessStopping         MachineReadiness = "stopping"
	MachineReadinessVerifying        MachineReadiness = "verifying"
	MachineReadinessRecoveryRequired MachineReadiness = "recovery_required"
)

type State struct {
	ConnectionStatus        ConnectionStatus
	EStopStatus             EStopStatus
	EStopSource             EStopSource
	PortName                string
	MachineState            string
	MachinePosition         [3]float64
	HasMachinePosition      bool
	WorkPosition            [3]float64
	HasWorkPosition         bool
	WorkCoordinateOffset    [3]float64
	HasWorkCoordinateOffset bool
	Feed                    float64
	Spindle                 float64
	HasFeedSpindle          bool
	LastStatusRaw           string
	LastError               string
	// MachineReadiness independently gates execution after an interrupted run.
	// MachineReadinessReason explains any non-normal restriction.
	MachineReadiness       MachineReadiness
	MachineReadinessReason string
	ProgramPath            string
	ProgramName            string
	ProgramDocumentID      DocumentID
	ProgramRunID           RunID
	// ProgramError belongs only to ProgramRunID. General controller errors use
	// LastError and must not modify this field.
	ProgramError    string
	ProgramStatus   ProgramStatus
	ProgramTotal    int
	ProgramComplete int
}

func (s State) IsConnected() bool {
	return s.ConnectionStatus == ConnectionConnected
}

func (s State) IsConnecting() bool {
	return s.ConnectionStatus == ConnectionConnecting
}

type Event struct {
	Kind EventKind
	When time.Time
	Text string
	Err  error
	// ErrorRunID explicitly associates an EventError with a program failure.
	// A zero value means the error is general; State.ProgramRunID remains only
	// contextual in that case. ErrorDocumentID may be zero for a legacy run.
	ErrorDocumentID DocumentID
	ErrorRunID      RunID
	State           State
	StateRevision   StateRevision
	Ports           []AvailablePort
	Raw             transport.Event
}
