package frontend

import "sync"

// ProgramStartGate accounts for frontend program-start requests between their
// synchronous UI admission and the return from controller admission. It is a
// shutdown coordination aid, not a replacement for controller admission.
type ProgramStartGate struct {
	mu          sync.Mutex
	closed      bool
	finalized   bool
	outstanding uint64
}

// ProgramStartAdmission is one accepted frontend start request. Release is
// idempotent so duplicate deferred cleanup cannot corrupt the outstanding
// count.
type ProgramStartAdmission struct {
	gate *ProgramStartGate
	once sync.Once
}

// Acquire synchronously registers a start request before its controller
// goroutine is launched. It rejects new requests while shutdown owns the gate.
func (g *ProgramStartGate) Acquire() (*ProgramStartAdmission, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, false
	}
	g.outstanding++
	return &ProgramStartAdmission{gate: g}, true
}

func (a *ProgramStartAdmission) Release() {
	if a == nil || a.gate == nil {
		return
	}
	a.once.Do(func() {
		a.gate.mu.Lock()
		defer a.gate.mu.Unlock()
		if a.gate.outstanding > 0 {
			a.gate.outstanding--
		}
	})
}

// Freeze rejects later starts while retaining accounting for requests already
// admitted before shutdown began.
func (g *ProgramStartGate) Freeze() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

// Reopen permits new requests after a cancelled shutdown. A committed final
// shutdown is deliberately irreversible.
func (g *ProgramStartGate) Reopen() {
	g.mu.Lock()
	if !g.finalized {
		g.closed = false
	}
	g.mu.Unlock()
}

// Finalize permanently closes admission immediately before application exit.
func (g *ProgramStartGate) Finalize() {
	g.mu.Lock()
	g.closed = true
	g.finalized = true
	g.mu.Unlock()
}

func (g *ProgramStartGate) Outstanding() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.outstanding
}

func (g *ProgramStartGate) Quiescent() bool { return g.Outstanding() == 0 }
