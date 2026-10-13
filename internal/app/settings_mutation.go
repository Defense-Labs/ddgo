package app

import (
	"context"

	"github.com/ianbruene/ddgo/internal/grbl"
	"github.com/ianbruene/ddgo/internal/transport"
)

type settingsMutationID uint64

type pendingSettingsMutation struct {
	id         settingsMutationID
	generation transport.ConnectionGeneration
	owner      responseOwner
	number     int
	value      float64
}

type commandPreparation struct {
	silenceID expectedSilenceID
	mutation  settingsMutationID
}

func commandLineNeedsLockedPreparation(line string) bool {
	if grbl.ClassifySilentSystemCommand(line) != grbl.SilentSystemNone {
		return true
	}
	if _, _, ok := grbl.ParseSettingAssignment(line); ok {
		return true
	}
	return grbl.ClassifySettingsReset(line) != grbl.SettingsResetNone
}

func (c *Controller) beginSettingsMutationLocked(owner responseOwner, line string) (settingsMutationID, error) {
	switch grbl.ClassifySettingsReset(line) {
	case grbl.SettingsResetGlobal, grbl.SettingsResetAll:
		// The firmware reset may interrupt the ordinary terminal response. Once
		// admitted, stale settings are less safe than temporarily having none.
		c.clearGRBLSettingsLocked()
		return 0, nil
	case grbl.SettingsResetCoordinates:
		return 0, nil
	}
	number, value, ok := grbl.ParseSettingAssignment(line)
	if !ok {
		return 0, nil
	}
	if owner.kind == responseOwnerNone || c.responseOwner != owner || owner.generation == 0 ||
		owner.generation != c.connectionGeneration || c.pendingSettingsMutation.id != 0 {
		return 0, ErrCommandSessionInvariant
	}
	c.nextSettingsMutationID++
	if c.nextSettingsMutationID == 0 {
		c.nextSettingsMutationID++
	}
	id := c.nextSettingsMutationID
	c.pendingSettingsMutation = pendingSettingsMutation{
		id:         id,
		generation: owner.generation,
		owner:      owner,
		number:     number,
		value:      value,
	}
	return id, nil
}

func (c *Controller) clearSettingsMutationLocked(id settingsMutationID) bool {
	if id == 0 || c.pendingSettingsMutation.id != id {
		return false
	}
	c.pendingSettingsMutation = pendingSettingsMutation{}
	return true
}

func (c *Controller) clearSettingsMutation(id settingsMutationID) {
	if id == 0 {
		return
	}
	c.mu.Lock()
	c.clearSettingsMutationLocked(id)
	c.mu.Unlock()
}

func (c *Controller) clearSettingsMutationForOwnerLocked(owner responseOwner) bool {
	if c.pendingSettingsMutation.id == 0 || c.pendingSettingsMutation.owner != owner {
		return false
	}
	c.pendingSettingsMutation = pendingSettingsMutation{}
	return true
}

func (c *Controller) commitSettingsMutationLocked(id settingsMutationID) bool {
	mutation := c.pendingSettingsMutation
	if id == 0 || mutation.id != id {
		return false
	}
	c.pendingSettingsMutation = pendingSettingsMutation{}
	if mutation.generation == 0 || mutation.generation != c.connectionGeneration ||
		c.grblSettings.generation != mutation.generation || c.grblSettings.values == nil {
		return false
	}
	c.grblSettings.values[mutation.number] = mutation.value
	return true
}

func (c *Controller) commitSettingsMutation(id settingsMutationID) bool {
	if id == 0 {
		return false
	}
	c.mu.Lock()
	committed := c.commitSettingsMutationLocked(id)
	c.mu.Unlock()
	return committed
}

func (c *Controller) commitSettingsMutationForOwnerLocked(owner responseOwner) bool {
	if c.pendingSettingsMutation.id == 0 || c.pendingSettingsMutation.owner != owner {
		return false
	}
	return c.commitSettingsMutationLocked(c.pendingSettingsMutation.id)
}

func (c *Controller) prepareCommandLocked(owner responseOwner, line string) (commandPreparation, error) {
	mutationID, err := c.beginSettingsMutationLocked(owner, line)
	if err != nil {
		return commandPreparation{}, err
	}
	preparation := commandPreparation{mutation: mutationID}
	if spec, ok := c.expectedSilenceSpecForLineLocked(line); ok {
		preparation.silenceID, err = c.beginExpectedSilenceLocked(owner, spec)
		if err != nil {
			c.clearSettingsMutationLocked(mutationID)
			return commandPreparation{}, err
		}
	}
	return preparation, nil
}

func (c *Controller) clearCommandPreparation(preparation commandPreparation) {
	c.endExpectedSilence(preparation.silenceID)
	c.clearSettingsMutation(preparation.mutation)
}

func (r *commandRuntime) prepareCommandForLine(ctx context.Context, line string) (commandPreparation, error) {
	c := r.controller
	c.mu.RLock()
	override := c.expectedSilencePolicy != nil
	c.mu.RUnlock()
	if !override && !commandLineNeedsLockedPreparation(line) {
		return commandPreparation{}, nil
	}
	if err := c.beginForegroundAdmission(ctx); err != nil {
		return commandPreparation{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	defer c.endForegroundAdmissionLocked()
	if err := ctx.Err(); err != nil {
		return commandPreparation{}, err
	}
	owner := c.responseOwner
	expectedKind := responseOwnerInteractiveMacro
	valid := owner.kind == expectedKind && owner.session == r.session
	if r.run != nil {
		expectedKind = responseOwnerProgram
		valid = owner.kind == expectedKind && owner.session == r.session && c.run == r.run && owner.generation == r.run.generation
	}
	if !valid {
		return commandPreparation{}, ErrCommandSessionInvariant
	}
	return c.prepareCommandLocked(owner, line)
}
