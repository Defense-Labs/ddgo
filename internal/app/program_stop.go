package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ianbruene/ddgo/internal/grbl"
	"github.com/ianbruene/ddgo/internal/transport"
)

var errResetBannerNotObserved = errors.New("controller reset could not be confirmed by a startup banner")
var errPostResetIdleNotObserved = errors.New("controller reset was not followed by a fresh Idle status")

type programStopOperation struct {
	id                      stopOperationID
	run                     *programRun
	generation              transport.ConnectionGeneration
	alarmBaseline           uint64
	done                    chan struct{}
	err                     error
	completed               bool
	invalidated             bool
	verificationInvalidated bool
	verificationErr         error
	resetWriteIssued        bool
}

// noteStopVerificationAlarmLocked records every current-generation alarm,
// including standalone ALARM:n feedback. A later Idle cannot prove readiness
// for a reset attempt once the firmware has reported an alarm.
func (c *Controller) noteStopVerificationAlarmLocked(generation transport.ConnectionGeneration, alarmCode int, err error) {
	if generation == 0 || generation != c.connectionGeneration {
		return
	}
	// GG3's mc_reset() deliberately raises EXEC_ALARM_ABORT_CYCLE (ALARM:3)
	// when Ctrl-X interrupts motion. Accept only that reset-associated report
	// from the active Stop coordinator; banner and fresh-Idle verification still
	// remain mandatory, and every other alarm invalidates the attempt.
	if alarmCode == 3 {
		if op := c.stopOperation; op != nil && !op.completed && !op.invalidated &&
			op.generation == generation && op.resetWriteIssued {
			return
		}
	}
	c.alarmRevision++
	close(c.alarmChanged)
	c.alarmChanged = make(chan struct{})
	if op := c.stopOperation; op != nil && !op.completed && op.generation == generation {
		op.verificationInvalidated = true
		if op.verificationErr == nil {
			op.verificationErr = err
		}
	}
}

func (c *Controller) markStopResetWriteIssued(op *programStopOperation) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopOperation != op || c.run != op.run || c.connectionGeneration != op.generation || op.invalidated {
		return false
	}
	op.resetWriteIssued = true
	return true
}

func (c *Controller) stopVerificationErrorLocked(op *programStopOperation) error {
	if op == nil || c.stopOperation != op || op.completed || op.invalidated {
		return ErrCommandSessionCanceled
	}
	if op.verificationInvalidated || c.alarmRevision > op.alarmBaseline {
		if op.verificationErr != nil {
			return op.verificationErr
		}
		return errors.New("controller alarm invalidated reset verification")
	}
	return nil
}

func (c *Controller) setMachineReadinessLocked(readiness MachineReadiness, reason string) {
	c.state.MachineReadiness = readiness
	c.state.MachineReadinessReason = reason
	if readiness == MachineReadinessStopping || readiness == MachineReadinessVerifying {
		c.statusWatchdogStartedAt = time.Time{}
		c.pendingQuietStatusReports = 0
	}
}

func (c *Controller) requireMachineRecoveryLocked(reason string) {
	if reason == "" {
		reason = "program execution ended without confirmed machine readiness; reconnect and verify controller Idle"
	}
	c.setMachineReadinessLocked(MachineReadinessRecoveryRequired, reason)
}

func (c *Controller) completeStopOperationLocked(op *programStopOperation, err error) {
	if op == nil || op.completed {
		return
	}
	op.err = err
	op.completed = true
	close(op.done)
	if c.stopOperation == op {
		c.stopOperation = nil
	}
}

// acquireStopWrite gives an accepted Stop priority over future status and
// foreground work while waiting for an already-admitted write to finish.
func (c *Controller) acquireStopWrite(ctx context.Context, op *programStopOperation) error {
	c.mu.Lock()
	c.foregroundAdmissionWaiters++
	for {
		if c.stopOperation != op || c.run != op.run || c.connectionGeneration != op.generation || !c.state.IsConnected() {
			c.endForegroundAdmissionLocked()
			c.mu.Unlock()
			return ErrCommandSessionCanceled
		}
		if !c.realtimeWriteActive {
			if err := c.beginRealtimeWriteForAdmissionLocked(admissionStop); err != nil {
				c.endForegroundAdmissionLocked()
				c.mu.Unlock()
				return err
			}
			c.endForegroundAdmissionLocked()
			c.mu.Unlock()
			return nil
		}
		done := c.realtimeWriteDone
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			c.mu.Lock()
			c.endForegroundAdmissionLocked()
			c.mu.Unlock()
			return ctx.Err()
		case <-done:
		}
		c.mu.Lock()
	}
}

func (c *Controller) executeProgramStop(op *programStopOperation) {
	transportCtx, cancelTransport := context.WithTimeout(context.Background(), c.stopTransportTimeout)
	err := c.acquireStopWrite(transportCtx, op)
	cancelTransport()
	if err != nil {
		c.failProgramStop(op, fmt.Errorf("acquire controller transport for stop: %w", err))
		return
	}
	c.mu.RLock()
	reservation := c.realtimeWriteDone
	c.mu.RUnlock()
	writeReserved := true
	defer func() {
		if writeReserved {
			c.endRealtimeWriteReservation(reservation)
		}
	}()

	var firstWriteErr error
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), c.stopTransportTimeout)
	pendingWrite, err := c.writeStopActionBounded(writeCtx, grbl.ActionHold)
	cancelWrite()
	if pendingWrite != nil {
		c.failProgramStop(op, fmt.Errorf("write program hold: %w", err))
		writeReserved = false
		go func() {
			<-pendingWrite
			c.endRealtimeWriteReservation(reservation)
		}()
		return
	}
	if err != nil {
		firstWriteErr = fmt.Errorf("write program hold: %w", err)
	}
	c.mu.RLock()
	stopStillCurrent := c.stopOperation == op && c.run == op.run && c.connectionGeneration == op.generation && !op.invalidated
	bannerBaseline := c.startupBannerRevision
	resetStartedAt := c.now()
	c.mu.RUnlock()
	if !stopStillCurrent {
		return
	}
	if !c.markStopResetWriteIssued(op) {
		return
	}
	writeCtx, cancelWrite = context.WithTimeout(context.Background(), c.stopTransportTimeout)
	pendingWrite, err = c.writeStopActionBounded(writeCtx, grbl.ActionSoftReset)
	cancelWrite()
	if pendingWrite != nil {
		resetErr := fmt.Errorf("write program soft reset: %w", err)
		if firstWriteErr != nil {
			resetErr = errors.Join(firstWriteErr, resetErr)
		}
		c.failProgramStop(op, resetErr)
		writeReserved = false
		go func() {
			<-pendingWrite
			c.endRealtimeWriteReservation(reservation)
		}()
		return
	}
	if err != nil && firstWriteErr == nil {
		firstWriteErr = fmt.Errorf("write program soft reset: %w", err)
	}
	c.endRealtimeWriteReservation(reservation)
	writeReserved = false
	if firstWriteErr != nil {
		c.failProgramStop(op, firstWriteErr)
		return
	}

	c.mu.Lock()
	if c.stopOperation != op || c.run != op.run || c.connectionGeneration != op.generation || op.invalidated {
		c.mu.Unlock()
		return
	}
	c.setMachineReadinessLocked(MachineReadinessVerifying, "program reset sent; waiting for controller restart and fresh Idle")
	verifying := c.captureEventStateLocked()
	c.mu.Unlock()
	c.events <- Event{Kind: EventStateChanged, When: time.Now(), State: verifying.state, StateRevision: verifying.revision, Text: "verifying controller after program reset"}

	bannerCtx, cancelBanner := context.WithTimeout(context.Background(), c.stopResetBannerTimeout)
	err = c.waitForStartupBannerAfter(bannerCtx, op, bannerBaseline, resetStartedAt)
	cancelBanner()
	if err != nil {
		c.failProgramStop(op, fmt.Errorf("%w: %v", errResetBannerNotObserved, err))
		return
	}

	idleCtx, cancelIdle := context.WithTimeout(context.Background(), c.stopIdleTimeout)
	err = c.verifyFreshIdle(idleCtx, op.generation, op.alarmBaseline, op)
	cancelIdle()
	if err != nil {
		c.failProgramStop(op, fmt.Errorf("%w: %v", errPostResetIdleNotObserved, err))
		return
	}
	c.finishProgramStop(op)
}

// writeStopActionBounded bounds the coordinator's wait even if an underlying
// serial write is stuck inside the operating-system call. A non-nil channel
// means the physical write is still in flight and retains the reservation.
func (c *Controller) writeStopActionBounded(ctx context.Context, action grbl.Action) (<-chan error, error) {
	msg, err := grbl.BuildAction(action)
	if err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- c.transport.Write(ctx, msg) }()
	select {
	case err := <-done:
		return nil, err
	case <-ctx.Done():
		return done, ctx.Err()
	}
}

func (c *Controller) waitForStartupBannerAfter(ctx context.Context, op *programStopOperation, baseline uint64, notBefore time.Time) error {
	for {
		c.mu.RLock()
		generation := op.generation
		if generation == 0 || c.connectionGeneration != generation || !c.state.IsConnected() {
			c.mu.RUnlock()
			return ErrTransportDisconnected
		}
		if err := c.stopVerificationErrorLocked(op); err != nil {
			c.mu.RUnlock()
			return err
		}
		revision := c.startupBannerRevision
		bannerAt := c.lastStartupBannerAt
		changed := c.startupBannerChanged
		alarmChanged := c.alarmChanged
		c.mu.RUnlock()
		if revision > baseline && !bannerAt.Before(notBefore) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		case <-alarmChanged:
		}
	}
}

func (c *Controller) verifyFreshIdle(ctx context.Context, generation transport.ConnectionGeneration, alarmBaseline uint64, op *programStopOperation) error {
	c.mu.RLock()
	if generation == 0 || c.connectionGeneration != generation || !c.state.IsConnected() {
		c.mu.RUnlock()
		return ErrTransportDisconnected
	}
	baseline := c.statusReportRevision
	if op != nil {
		if err := c.stopVerificationErrorLocked(op); err != nil {
			c.mu.RUnlock()
			return err
		}
	} else if c.alarmRevision > alarmBaseline {
		c.mu.RUnlock()
		return errors.New("controller alarm invalidated readiness verification")
	}
	c.mu.RUnlock()
	if err := c.writeStatusPollWhenAvailable(ctx); err != nil {
		return err
	}
	for {
		c.mu.RLock()
		if c.connectionGeneration != generation || !c.state.IsConnected() {
			c.mu.RUnlock()
			return ErrTransportDisconnected
		}
		if op != nil {
			if err := c.stopVerificationErrorLocked(op); err != nil {
				c.mu.RUnlock()
				return err
			}
		} else if c.alarmRevision > alarmBaseline {
			c.mu.RUnlock()
			return errors.New("controller alarm invalidated readiness verification")
		}
		revision := c.statusReportRevision
		machineState := c.state.MachineState
		changed := c.statusReportChanged
		alarmChanged := c.alarmChanged
		c.mu.RUnlock()
		if revision > baseline {
			if strings.EqualFold(machineState, "Alarm") {
				return errors.New("machine reported Alarm")
			}
			if strings.EqualFold(machineState, "Idle") {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		case <-alarmChanged:
		}
	}
}

func (c *Controller) finishProgramStop(op *programStopOperation) {
	c.mu.Lock()
	if c.stopOperation != op || op.completed {
		c.mu.Unlock()
		return
	}
	if err := c.stopVerificationErrorLocked(op); err != nil || c.run != op.run || c.connectionGeneration != op.generation ||
		c.state.EStopStatus != EStopClear || c.state.MachineReadiness != MachineReadinessVerifying {
		if err == nil {
			err = errors.New("controller reset readiness could not be finalized")
		}
		c.mu.Unlock()
		c.failProgramStop(op, err)
		return
	}
	c.run = nil
	c.releaseResponseOwnerLocked(responseOwnerProgram, op.run.responseSession())
	c.state.ProgramStatus = ProgramStopped
	c.state.ProgramError = ""
	c.setMachineReadinessLocked(MachineReadinessNormal, "")
	c.completeStopOperationLocked(op, nil)
	snapshot := c.captureEventStateLocked()
	c.mu.Unlock()
	c.events <- Event{Kind: EventStateChanged, When: time.Now(), State: snapshot.state, StateRevision: snapshot.revision, Text: "program stopped"}
}

func (c *Controller) failProgramStop(op *programStopOperation, err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	if c.stopOperation != op || op.completed {
		c.mu.Unlock()
		return
	}
	if c.run == op.run {
		c.run = nil
		c.releaseResponseOwnerLocked(responseOwnerProgram, op.run.responseSession())
		c.recordProgramFailureLocked(op.run, err)
	}
	c.requireMachineRecoveryLocked(err.Error() + "; reconnect and verify controller Idle")
	c.completeStopOperationLocked(op, err)
	snapshot := c.captureEventStateLocked()
	c.mu.Unlock()
	op.run.cancel()
	c.events <- Event{Kind: EventStateChanged, When: time.Now(), State: snapshot.state, StateRevision: snapshot.revision, Text: "program stop could not be verified"}
	c.events <- programErrorEvent(err, snapshot, op.run)
}

func (c *Controller) verifyReadinessAfterConnect(generation transport.ConnectionGeneration, alarmBaseline uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), c.stopIdleTimeout)
	err := c.verifyFreshIdle(ctx, generation, alarmBaseline, nil)
	cancel()
	c.mu.Lock()
	if c.connectionGeneration != generation || c.state.MachineReadiness != MachineReadinessVerifying {
		c.mu.Unlock()
		return
	}
	if c.alarmRevision > alarmBaseline {
		err = errors.New("controller alarm invalidated reconnect readiness verification")
	}
	if err != nil {
		c.requireMachineRecoveryLocked("reconnected controller did not provide a fresh Idle status: " + err.Error())
	} else if c.state.EStopStatus == EStopClear {
		c.setMachineReadinessLocked(MachineReadinessNormal, "")
	}
	snapshot := c.captureEventStateLocked()
	c.mu.Unlock()
	text := "machine readiness restored after validated reconnect"
	if err != nil {
		text = c.Snapshot().MachineReadinessReason
	}
	c.events <- Event{Kind: EventStateChanged, When: time.Now(), State: snapshot.state, StateRevision: snapshot.revision, Text: text}
}
