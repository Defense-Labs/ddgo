package frontend

import "context"

// LegacyProgramCoordinator serializes the main-window legacy Load and Run
// requests on the UI thread. It makes the operator's selected program
// unambiguous without replacing controller admission or program ownership.
type LegacyProgramCoordinator struct {
	nextID      uint64
	pendingLoad uint64
	pendingRun  uint64
}

// LegacyProgramLoadResult and LegacyProgramRunResult carry completion from a
// controller goroutine back to the UI thread. The request ID prevents an old
// result from completing a newer frontend operation.
type LegacyProgramLoadResult struct {
	ID  uint64
	Err error
}

type LegacyProgramRunResult struct {
	ID  uint64
	Err error
}

// LegacyProgramRequests is the toolkit-neutral orchestration used by the
// main-window legacy Load and Run callbacks. It registers intent before
// launching a goroutine, then returns matching completion results for UI-thread
// consumption. The supplied operations remain responsible for controller
// admission; this helper only preserves frontend ordering.
type LegacyProgramRequests struct {
	Coordinator *LegacyProgramCoordinator
	StartGate   *ProgramStartGate
	Load        func(path string) error
	Start       func(context.Context) error
	OnChange    func()
	OnLoadDone  func(LegacyProgramLoadResult)
	OnRunDone   func(LegacyProgramRunResult)
}

func (r *LegacyProgramRequests) RequestLoad(path string) bool {
	if r == nil || r.Coordinator == nil || r.Load == nil || r.OnLoadDone == nil {
		return false
	}
	id, ok := r.Coordinator.BeginLoad()
	if !ok {
		return false
	}
	r.changed()
	go func() {
		r.OnLoadDone(LegacyProgramLoadResult{ID: id, Err: r.Load(path)})
	}()
	return true
}

func (r *LegacyProgramRequests) RequestRun() bool {
	if r == nil || r.Coordinator == nil || r.StartGate == nil || r.Start == nil || r.OnRunDone == nil {
		return false
	}
	id, ok := r.Coordinator.BeginRun()
	if !ok {
		return false
	}
	admission, ok := r.StartGate.Acquire()
	if !ok {
		r.Coordinator.FinishRun(id)
		return false
	}
	r.changed()
	go func() {
		// Keep the gate registered until controller admission has returned.
		err := r.Start(context.Background())
		r.OnRunDone(LegacyProgramRunResult{ID: id, Err: err})
		admission.Release()
	}()
	return true
}

func (r *LegacyProgramRequests) FinishLoad(result LegacyProgramLoadResult) bool {
	if r == nil || r.Coordinator == nil || !r.Coordinator.FinishLoad(result.ID) {
		return false
	}
	r.changed()
	return true
}

func (r *LegacyProgramRequests) FinishRun(result LegacyProgramRunResult) bool {
	if r == nil || r.Coordinator == nil || !r.Coordinator.FinishRun(result.ID) {
		return false
	}
	r.changed()
	return true
}

func (r *LegacyProgramRequests) changed() {
	if r.OnChange != nil {
		r.OnChange()
	}
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
