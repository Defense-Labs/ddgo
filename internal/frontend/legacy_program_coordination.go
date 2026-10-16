package frontend

// LegacyProgramCoordinator serializes the main-window legacy Load and Run
// requests on the UI thread. It makes the operator's selected program
// unambiguous without replacing controller admission or program ownership.
type LegacyProgramCoordinator struct {
	nextID      uint64
	pendingLoad uint64
	pendingRun  uint64
}

func (c *LegacyProgramCoordinator) BeginLoad() (uint64, bool) {
	if c.pendingLoad != 0 || c.pendingRun != 0 {
		return 0, false
	}
	c.pendingLoad = c.next()
	return c.pendingLoad, true
}

func (c *LegacyProgramCoordinator) FinishLoad(id uint64) bool {
	if id == 0 || c.pendingLoad != id {
		return false
	}
	c.pendingLoad = 0
	return true
}

func (c *LegacyProgramCoordinator) BeginRun() (uint64, bool) {
	if c.pendingLoad != 0 || c.pendingRun != 0 {
		return 0, false
	}
	c.pendingRun = c.next()
	return c.pendingRun, true
}

func (c *LegacyProgramCoordinator) FinishRun(id uint64) bool {
	if id == 0 || c.pendingRun != id {
		return false
	}
	c.pendingRun = 0
	return true
}

func (c *LegacyProgramCoordinator) LoadPending() bool { return c.pendingLoad != 0 }
func (c *LegacyProgramCoordinator) RunPending() bool  { return c.pendingRun != 0 }

func (c *LegacyProgramCoordinator) next() uint64 {
	c.nextID++
	if c.nextID == 0 {
		c.nextID++
	}
	return c.nextID
}
