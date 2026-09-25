package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ianbruene/ddgo/internal/transport"
)

const defaultControllerResponseTimeout = 2 * time.Second

type eStopTransitionResult struct {
	entered  bool
	cancel   context.CancelFunc
	error    error
	snapshot versionedState
}

func emergencyStopError(source EStopSource) error {
	switch source {
	case EStopSourceAlarm50:
		return fmt.Errorf("%w: controller reported ALARM:50", ErrEmergencyStop)
	default:
		return fmt.Errorf("%w: controller stopped responding", ErrEmergencyStop)
	}
}

func (c *Controller) enterEStop(source EStopSource, generation transport.ConnectionGeneration) bool {
	c.mu.Lock()
	result := c.enterEStopLocked(source, generation)
	c.mu.Unlock()
	c.finishEStopTransition(result)
	return result.entered
}

// enterEStopLocked is the single transition into the latched emergency-stop
// state. It deliberately leaves the transport and committed generation intact.
func (c *Controller) enterEStopLocked(source EStopSource, generation transport.ConnectionGeneration) eStopTransitionResult {
	if generation == 0 || generation != c.connectionGeneration || !c.state.IsConnected() {
		return eStopTransitionResult{}
	}
	if source == EStopSourceUnresponsive && c.statusMonitoringGeneration != generation {
		return eStopTransitionResult{}
	}
	if c.state.EStopStatus == EStopActive {
		return eStopTransitionResult{}
	}

	err := emergencyStopError(source)
	run := c.run
	var cancel context.CancelFunc
	if run != nil {
		c.run = nil
		cancel = run.cancel
	}
	if run != nil || c.state.ProgramStatus.IsActive() {
		c.state.ProgramStatus = ProgramFailed
	}
	c.terminateResponseOwnerLocked(c.responseOwner, err)
	c.contour.Disable()
	c.invalidateMachineTelemetryLocked()
	c.state.EStopStatus = EStopActive
	c.state.EStopSource = source
	c.state.LastError = err.Error()

	return eStopTransitionResult{
		entered:  true,
		cancel:   cancel,
		error:    err,
		snapshot: c.captureEventStateLocked(),
	}
}

func (c *Controller) finishEStopTransition(result eStopTransitionResult) {
	if !result.entered {
		return
	}
	if result.cancel != nil {
		result.cancel()
	}
	c.events <- Event{
		Kind:          EventStateChanged,
		When:          time.Now(),
		State:         result.snapshot.state,
		StateRevision: result.snapshot.revision,
		Text:          result.error.Error(),
	}
	c.events <- Event{
		Kind:          EventError,
		When:          time.Now(),
		Err:           result.error,
		Text:          result.error.Error(),
		State:         result.snapshot.state,
		StateRevision: result.snapshot.revision,
	}
}

func (c *Controller) invalidateMachineTelemetryLocked() {
	c.state.MachineState = ""
	c.state.MachinePosition = [3]float64{}
	c.state.HasMachinePosition = false
	c.state.WorkPosition = [3]float64{}
	c.state.HasWorkPosition = false
	c.state.WorkCoordinateOffset = [3]float64{}
	c.state.HasWorkCoordinateOffset = false
	c.state.Feed = 0
	c.state.Spindle = 0
	c.state.HasFeedSpindle = false
	c.state.LastStatusRaw = ""
}

func (c *Controller) noteStatusResponseLocked(generation transport.ConnectionGeneration, machineState string) (bool, versionedState, string) {
	if generation == 0 || generation != c.connectionGeneration || generation != c.statusMonitoringGeneration {
		return false, versionedState{}, ""
	}
	c.lastStatusResponseTime = c.now()
	// A valid status report satisfies the outstanding heartbeat request. A new
	// deadline begins only after a later status poll is actually admitted.
	c.statusWatchdogStartedAt = time.Time{}
	if c.state.EStopStatus == EStopClear {
		return false, versionedState{}, ""
	}

	if strings.EqualFold(strings.TrimSpace(machineState), "Idle") {
		c.state.EStopStatus = EStopClear
		c.state.EStopSource = EStopSourceNone
		c.state.LastError = ""
		return true, c.captureEventStateLocked(), "emergency stop cleared"
	}
	if c.state.EStopStatus == EStopActive {
		c.state.EStopStatus = EStopRecovery
		return true, c.captureEventStateLocked(), "emergency stop recovery: controller responding"
	}
	return false, versionedState{}, ""
}

func (c *Controller) resetControllerHealthLocked() {
	c.statusMonitoringGeneration = 0
	c.statusWatchdogStartedAt = time.Time{}
	c.lastStatusResponseTime = time.Time{}
}

func (c *Controller) checkStatusWatchdog(generation transport.ConnectionGeneration, now time.Time) {
	c.mu.RLock()
	if generation == 0 || generation != c.connectionGeneration || generation != c.statusMonitoringGeneration ||
		!c.state.IsConnected() || c.statusWatchdogStartedAt.IsZero() || c.state.EStopStatus == EStopActive {
		c.mu.RUnlock()
		return
	}
	deadlineBase := c.statusWatchdogStartedAt
	timeout := c.controllerResponseTimeout
	c.mu.RUnlock()
	if timeout > 0 && now.Sub(deadlineBase) >= timeout {
		c.enterEStop(EStopSourceUnresponsive, generation)
	}
}

func statusWatchdogInterval(timeout time.Duration) time.Duration {
	interval := timeout / 4
	if interval <= 0 {
		return time.Millisecond
	}
	if interval > 100*time.Millisecond {
		return 100 * time.Millisecond
	}
	return interval
}
