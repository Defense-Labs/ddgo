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

const (
	defaultConnectionBannerTimeout        = 3 * time.Second
	defaultConnectionStartupQuietPeriod   = 250 * time.Millisecond
	defaultConnectionStartupSettleTimeout = 3 * time.Second
	defaultConnectionSettingsTimeout      = 3 * time.Second
	connectAttemptEventCapacity           = 256
	maxSettingsResponseLines              = 256
)

// ErrConnectAttemptEventOverflow reports that untrusted startup traffic
// exceeded the bounded private queue for a connection attempt.
var ErrConnectAttemptEventOverflow = errors.New("connection handshake event queue overflow")

// captureConnectAttemptEventLocked diverts untrusted pre-commit traffic into
// the active attempt. Before Open returns, every non-zero generation must be
// retained; once the candidate is known, stale generations are consumed and
// ignored. c.mu must be held.
func (c *Controller) captureConnectAttemptEventLocked(event transport.Event) bool {
	if c.connectionTransition != connectionConnecting || c.activeConnectAttempt == 0 {
		return false
	}
	if event.Generation == 0 {
		return true
	}
	if c.connectAttemptGeneration != 0 && event.Generation != c.connectAttemptGeneration {
		return true
	}
	if c.connectAttemptEvents == nil {
		c.setConnectAttemptErrorLocked(ErrConnectionInvariant)
		return true
	}
	select {
	case c.connectAttemptEvents <- event:
	default:
		c.setConnectAttemptErrorLocked(ErrConnectAttemptEventOverflow)
	}
	if c.connectAttemptGeneration != 0 && event.Generation == c.connectAttemptGeneration {
		switch event.Kind {
		case transport.EventDisconnected:
			c.setConnectAttemptErrorLocked(ErrTransportDisconnected)
		case transport.EventError:
			c.setConnectAttemptErrorLocked(connectTransportEventError(event))
		}
	}
	return true
}

func (c *Controller) setConnectAttemptErrorLocked(err error) {
	if err != nil && c.connectAttemptErr == nil {
		c.connectAttemptErr = err
	}
}

func (c *Controller) validateGRBLConnection(ctx context.Context, attempt connectAttemptID, generation transport.ConnectionGeneration) error {
	events, err := c.connectAttemptEventStream(attempt)
	if err != nil {
		return err
	}
	if err := c.waitForGRBLBanner(ctx, attempt, generation, events); err != nil {
		return err
	}
	if err := c.waitForStartupQuiet(ctx, attempt, generation, events); err != nil {
		return err
	}

	msg := transport.NewLineMessage("$$")
	msg.SuppressLog = true
	queryStarted := time.Now()
	if err := c.transport.Write(ctx, msg); err != nil {
		return fmt.Errorf("write GRBL settings query: %w", err)
	}
	return c.waitForSettingsResponse(ctx, attempt, generation, queryStarted, events)
}

func (c *Controller) connectAttemptEventStream(attempt connectAttemptID) (<-chan transport.Event, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if attempt == 0 || c.connectionTransition != connectionConnecting || c.activeConnectAttempt != attempt || c.connectAttemptEvents == nil {
		return nil, ErrConnectionInvariant
	}
	return c.connectAttemptEvents, nil
}

func (c *Controller) currentConnectAttemptError(attempt connectAttemptID) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if attempt == 0 || c.connectionTransition != connectionConnecting || c.activeConnectAttempt != attempt {
		return ErrConnectionInvariant
	}
	return c.connectAttemptErr
}

func (c *Controller) waitForGRBLBanner(ctx context.Context, attempt connectAttemptID, generation transport.ConnectionGeneration, events <-chan transport.Event) error {
	timer := time.NewTimer(c.connectionBannerTimeout)
	defer timer.Stop()
	for {
		if err := c.currentConnectAttemptError(attempt); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("timed out waiting for GRBL startup banner")
		case event := <-events:
			if event.Generation != generation {
				continue
			}
			switch event.Kind {
			case transport.EventRX:
				if _, ok := grbl.ParseStartupBanner(event.Text); ok {
					return nil
				}
			case transport.EventError:
				return connectTransportEventError(event)
			case transport.EventDisconnected:
				return ErrTransportDisconnected
			}
		}
	}
}

func (c *Controller) waitForStartupQuiet(ctx context.Context, attempt connectAttemptID, generation transport.ConnectionGeneration, events <-chan transport.Event) error {
	quiet := time.NewTimer(c.connectionStartupQuietPeriod)
	defer quiet.Stop()
	absolute := time.NewTimer(c.connectionStartupSettleTimeout)
	defer absolute.Stop()

	for {
		if err := c.currentConnectAttemptError(attempt); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-absolute.C:
			return errors.New("GRBL startup output did not settle")
		case <-quiet.C:
			// A timer and a queued line can become ready together. Drain anything
			// already queued before declaring the startup stream quiet.
			sawRX := false
		drainReady:
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-absolute.C:
					return errors.New("GRBL startup output did not settle")
				default:
				}
				select {
				case event := <-events:
					if event.Generation != generation {
						continue
					}
					switch event.Kind {
					case transport.EventRX:
						sawRX = true
					case transport.EventError:
						return connectTransportEventError(event)
					case transport.EventDisconnected:
						return ErrTransportDisconnected
					}
				default:
					break drainReady
				}
			}
			if !sawRX {
				return nil
			}
			resetTimer(quiet, c.connectionStartupQuietPeriod)
		case event := <-events:
			if event.Generation != generation {
				continue
			}
			switch event.Kind {
			case transport.EventRX:
				resetTimer(quiet, c.connectionStartupQuietPeriod)
			case transport.EventError:
				return connectTransportEventError(event)
			case transport.EventDisconnected:
				return ErrTransportDisconnected
			}
		}
	}
}

func (c *Controller) waitForSettingsResponse(ctx context.Context, attempt connectAttemptID, generation transport.ConnectionGeneration, queryStarted time.Time, events <-chan transport.Event) error {
	timer := time.NewTimer(c.connectionSettingsTimeout)
	defer timer.Stop()
	settingSeen := false
	lines := 0
	for {
		if err := c.currentConnectAttemptError(attempt); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("timed out waiting for GRBL settings response")
		case event := <-events:
			if event.Generation != generation {
				continue
			}
			switch event.Kind {
			case transport.EventError:
				return connectTransportEventError(event)
			case transport.EventDisconnected:
				return ErrTransportDisconnected
			case transport.EventRX:
				if !event.When.IsZero() && event.When.Before(queryStarted) {
					continue
				}
				lines++
				if lines > maxSettingsResponseLines {
					return errors.New("GRBL settings response exceeded line limit")
				}
				line := strings.TrimSpace(event.Text)
				lower := strings.ToLower(line)
				switch {
				case grbl.IsSettingLine(line):
					settingSeen = true
				case lower == "ok":
					if !settingSeen {
						return errors.New("GRBL settings response ended without any settings")
					}
					return nil
				case strings.HasPrefix(lower, "error"), strings.HasPrefix(lower, "alarm"):
					return fmt.Errorf("GRBL settings query failed: %s", line)
				}
			}
		}
	}
}

func connectTransportEventError(event transport.Event) error {
	if event.Err != nil {
		return event.Err
	}
	if event.Text != "" {
		return errors.New(event.Text)
	}
	return errors.New("transport error during connection handshake")
}

func (c *Controller) failConnectAttempt(attempt connectAttemptID, generation transport.ConnectionGeneration, primary error, closeCandidate bool, report bool) error {
	if primary == nil {
		primary = ErrConnectionInvariant
	}
	if closeCandidate {
		c.mu.Lock()
		if generation != 0 && c.activeConnectAttempt == attempt {
			c.suppressTransportDisconnectedGeneration = generation
		}
		c.mu.Unlock()
		if closeErr := c.transport.Close(); closeErr != nil {
			primary = errors.Join(primary, fmt.Errorf("close candidate transport: %w", closeErr))
		}
	}

	c.mu.Lock()
	emitDisconnected := c.state.ConnectionStatus == ConnectionConnecting
	var disconnected versionedState
	if emitDisconnected {
		c.state.ConnectionStatus = ConnectionDisconnected
		disconnected = c.captureEventStateLocked()
	}
	c.finishConnectAttemptLocked(attempt)
	c.mu.Unlock()
	if emitDisconnected {
		c.events <- Event{Kind: EventStateChanged, When: time.Now(), State: disconnected.state, StateRevision: disconnected.revision, Text: "disconnected"}
	}
	if report {
		c.emitError(primary)
	}
	return primary
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}
