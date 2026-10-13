package app

import "github.com/ianbruene/ddgo/internal/transport"

// grblSettingsSnapshot is the numeric settings observed while validating the
// currently committed transport generation. It is intentionally private until
// expected-silence timing policy is ready to consume it.
type grblSettingsSnapshot struct {
	generation transport.ConnectionGeneration
	values     map[int]float64
}

func cloneGRBLSettings(values map[int]float64) map[int]float64 {
	if values == nil {
		return nil
	}
	cloned := make(map[int]float64, len(values))
	for number, value := range values {
		cloned[number] = value
	}
	return cloned
}

func (c *Controller) setGRBLSettingsLocked(generation transport.ConnectionGeneration, values map[int]float64) {
	if generation == 0 {
		c.clearGRBLSettingsLocked()
		return
	}
	c.grblSettings = grblSettingsSnapshot{
		generation: generation,
		values:     cloneGRBLSettings(values),
	}
}

func (c *Controller) clearGRBLSettingsLocked() {
	c.grblSettings = grblSettingsSnapshot{}
}

// grblSettingsSnapshotLocked returns a detached copy suitable for passing to a
// pure timing calculator. The caller must hold c.mu.
func (c *Controller) grblSettingsSnapshotLocked() grblSettingsSnapshot {
	return grblSettingsSnapshot{
		generation: c.grblSettings.generation,
		values:     cloneGRBLSettings(c.grblSettings.values),
	}
}

func (c *Controller) grblSettingsSnapshotCopy() grblSettingsSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.grblSettingsSnapshotLocked()
}
