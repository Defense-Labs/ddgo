package app

import (
	"strings"
	"time"

	"github.com/ianbruene/ddgo/internal/grbl"
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
	levelPlan       expectedSilenceLevelPlan
}

type expectedSilenceLease struct {
	id              expectedSilenceID
	generation      transport.ConnectionGeneration
	owner           responseOwner
	class           expectedSilenceClass
	startedAt       time.Time
	lastActivity    time.Time
	inactivityLimit time.Duration
	levelPlan       expectedSilenceLevelPlan
	adjustmentCount int
}

// expectedSilenceSpecForLineLocked resolves the policy against the settings
// owned by the currently admitted generation. The caller must hold c.mu.
func (c *Controller) expectedSilenceSpecForLineLocked(line string) (expectedSilenceSpec, bool) {
	if c.expectedSilencePolicy != nil {
		return expectedSilenceSpecForPolicy(c.expectedSilencePolicy, line)
	}
	command := grbl.ClassifySilentSystemCommand(line)
	if command == grbl.SilentSystemNone || c.connectionGeneration == 0 ||
		c.grblSettings.generation != c.connectionGeneration {
		return expectedSilenceSpec{}, false
	}
	return gg3ExpectedSilenceSpec(command, c.grblSettings.values)
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
		levelPlan:       spec.levelPlan,
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

func (c *Controller) noteExpectedSilenceProgressLocked(generation transport.ConnectionGeneration, when time.Time, line string) {
	if when.IsZero() {
		when = c.now()
	}
	if !c.expectedSilenceActiveForGenerationLocked(generation) || c.expectedSilence.class != expectedSilenceLevel ||
		when.Before(c.expectedSilence.startedAt) || when.Before(c.expectedSilence.lastActivity) ||
		c.expectedSilence.adjustmentCount >= 3 {
		return
	}
	adjustment, parsed := grbl.ParseGG3LevelAdjustment(line)
	if !parsed && !grbl.IsGG3LevelAdjustmentLike(line) {
		return
	}
	nextCount := c.expectedSilence.adjustmentCount + 1
	var limit time.Duration
	var ok bool
	if parsed {
		limit, ok = gg3LevelNextInactivityLimit(c.expectedSilence.levelPlan, adjustment, nextCount)
	} else {
		limit, ok = gg3LevelFallbackInactivityLimit(c.expectedSilence.levelPlan, nextCount)
	}
	if !ok || limit <= 0 {
		return
	}
	c.expectedSilence.adjustmentCount = nextCount
	c.expectedSilence.inactivityLimit = limit
}
