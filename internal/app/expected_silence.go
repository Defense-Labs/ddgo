package app

import (
	"context"
	"strings"
	"time"

	"github.com/ianbruene/ddgo/internal/transport"
)

type expectedSilenceID uint64

type expectedSilenceClass uint8

const (
	expectedSilenceNone expectedSilenceClass = iota
	expectedSilenceHomeAll
	expectedSilenceHomeX
	expectedSilenceHomeY
	expectedSilenceHomeZ
	expectedSilenceLevel
	expectedSilenceLevelSave
)

type expectedSilencePolicy func(line string) (expectedSilenceClass, time.Duration, bool)

type expectedSilenceSpec struct {
	class           expectedSilenceClass
	inactivityLimit time.Duration
}

type expectedSilenceLease struct {
	id              expectedSilenceID
	generation      transport.ConnectionGeneration
	owner           responseOwner
	class           expectedSilenceClass
	startedAt       time.Time
	lastActivity    time.Time
	inactivityLimit time.Duration
}

func (c *Controller) expectedSilenceSpecForLine(line string) (expectedSilenceSpec, bool) {
	c.mu.RLock()
	policy := c.expectedSilencePolicy
	c.mu.RUnlock()
	return expectedSilenceSpecForPolicy(policy, line)
}

func expectedSilenceSpecForPolicy(policy expectedSilencePolicy, line string) (expectedSilenceSpec, bool) {
	if policy == nil {
		return expectedSilenceSpec{}, false
	}
	class, limit, ok := policy(strings.ToUpper(strings.TrimSpace(line)))
	if !ok {
		return expectedSilenceSpec{}, false
	}
	return expectedSilenceSpec{class: class, inactivityLimit: limit}, true
}

func (c *Controller) beginExpectedSilenceLocked(owner responseOwner, spec expectedSilenceSpec) (expectedSilenceID, error) {
	if spec.class == expectedSilenceNone || owner.kind == responseOwnerNone || c.responseOwner != owner ||
		owner.generation == 0 || owner.generation != c.connectionGeneration ||
		owner.generation != c.statusMonitoringGeneration || !c.state.IsConnected() ||
		c.expectedSilence.id != 0 {
		return 0, ErrCommandSessionInvariant
	}
	c.nextExpectedSilenceID++
	if c.nextExpectedSilenceID == 0 {
		c.nextExpectedSilenceID++
	}
	id := c.nextExpectedSilenceID
	c.expectedSilence = expectedSilenceLease{
		id:              id,
		generation:      owner.generation,
		owner:           owner,
		class:           spec.class,
		startedAt:       c.now(),
		inactivityLimit: spec.inactivityLimit,
	}
	c.statusWatchdogStartedAt = time.Time{}
	c.pendingQuietStatusReports = 0
	return id, nil
}

func (c *Controller) endExpectedSilenceLocked(id expectedSilenceID) bool {
	if id == 0 || c.expectedSilence.id != id {
		return false
	}
	c.expectedSilence = expectedSilenceLease{}
	return true
}

func (c *Controller) endExpectedSilence(id expectedSilenceID) {
	if id == 0 {
		return
	}
	c.mu.Lock()
	c.endExpectedSilenceLocked(id)
	c.mu.Unlock()
}

func (c *Controller) endExpectedSilenceForOwnerLocked(owner responseOwner) bool {
	if c.expectedSilence.id == 0 || c.expectedSilence.owner != owner {
		return false
	}
	c.expectedSilence = expectedSilenceLease{}
	return true
}

func (c *Controller) clearExpectedSilenceLocked() {
	c.expectedSilence = expectedSilenceLease{}
}

func (c *Controller) expectedSilenceActiveForGenerationLocked(generation transport.ConnectionGeneration) bool {
	return c.expectedSilence.id != 0 && generation != 0 && c.expectedSilence.generation == generation
}

func (c *Controller) noteExpectedSilenceActivityLocked(generation transport.ConnectionGeneration, when time.Time) {
	if !c.expectedSilenceActiveForGenerationLocked(generation) || when.Before(c.expectedSilence.startedAt) {
		return
	}
	if when.After(c.expectedSilence.lastActivity) {
		c.expectedSilence.lastActivity = when
	}
}

func (r *commandRuntime) beginExpectedSilenceForLine(ctx context.Context, line string) (expectedSilenceID, error) {
	c := r.controller
	spec, ok := c.expectedSilenceSpecForLine(line)
	if !ok {
		return 0, nil
	}
	if err := c.beginForegroundAdmission(ctx); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.endForegroundAdmissionLocked()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	owner := c.responseOwner
	expectedKind := responseOwnerInteractiveMacro
	valid := owner.kind == expectedKind && owner.session == r.session
	if r.run != nil {
		expectedKind = responseOwnerProgram
		valid = owner.kind == expectedKind && owner.session == r.session && c.run == r.run && owner.generation == r.run.generation
	}
	if !valid {
		return 0, ErrCommandSessionInvariant
	}
	return c.beginExpectedSilenceLocked(owner, spec)
}
