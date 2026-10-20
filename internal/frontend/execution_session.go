package frontend

import (
	"fmt"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/gcode"
)

// PackageStepIdentity identifies one ordered package job and one stable step
// within it. JobIndex is one-based, matching the package's loaded ordering.
// A resource filename is never an execution identity.
type PackageStepIdentity struct {
	JobIndex int
	StepID   string
}

// ExecutionSessionID identifies one retained G-code execution context. A
// standalone document has an empty PackageStep. Package steps deliberately
// carry their owning package document identity plus this typed package-step
// identity.
type ExecutionSessionID struct {
	DocumentID  app.DocumentID
	PackageStep PackageStepIdentity
}

func StandaloneExecutionSessionID(documentID app.DocumentID) ExecutionSessionID {
	return ExecutionSessionID{DocumentID: documentID}
}

func PackageStepExecutionSessionID(documentID app.DocumentID, jobIndex int, stepID string) ExecutionSessionID {
	return ExecutionSessionID{DocumentID: documentID, PackageStep: PackageStepIdentity{JobIndex: jobIndex, StepID: stepID}}
}

func (id ExecutionSessionID) Standalone() bool { return id.PackageStep == (PackageStepIdentity{}) }

func (id ExecutionSessionID) Valid() bool {
	return id.DocumentID != 0 && (id.Standalone() || (id.PackageStep.JobIndex > 0 && id.PackageStep.StepID != ""))
}

// ExecutionSessionSnapshot is the single presentation and command-policy
// input for every view attached to a session. It is a value copy so views do
// not retain mutable session state.
type ExecutionSessionSnapshot struct {
	ID               ExecutionSessionID
	Source           string
	Program          gcode.Program
	ParseError       error
	Presentation     DocumentExecutionPresentation
	State            app.State
	StartPending     bool
	OperationPending bool
	LastOperationErr error
	Controls         DocumentExecutionControls
}

// ExecutionSession holds execution state independently of native windows.
// It is intentionally UI-thread owned: Application delivers controller
// events/results and invokes command transitions on the UI thread; observers
// are consequently never called from controller goroutines.
type ExecutionSession struct {
	id           ExecutionSessionID
	source       string
	program      gcode.Program
	parseErr     error
	presentation DocumentExecutionPresentation
	state        app.State

	startPending     bool
	awaitingRunState app.RunID
	operationPending bool
	lastOperationErr error
	nextRequest      uint64
	startRequest     uint64
	actionRunID      app.RunID

	nextObserver uint64
	observers    map[uint64]func(ExecutionSessionSnapshot)
}

func NewExecutionSession(id ExecutionSessionID, source string, program gcode.Program, parseErr error, state app.State) (*ExecutionSession, error) {
	if !id.Valid() {
		return nil, fmt.Errorf("invalid execution session identity")
	}
	s := &ExecutionSession{
		id:           id,
		source:       source,
		program:      cloneExecutionProgram(program),
		parseErr:     parseErr,
		presentation: NewDocumentExecutionPresentation(id.DocumentID, cloneExecutionProgram(program)),
		state:        state,
	}
	if parseErr != nil {
		s.presentation.Error = parseErr.Error()
	}
	return s, nil
}

func (s *ExecutionSession) ID() ExecutionSessionID { return s.id }

func (s *ExecutionSession) Snapshot() ExecutionSessionSnapshot {
	return ExecutionSessionSnapshot{
		ID:               s.id,
		Source:           s.source,
		Program:          cloneExecutionProgram(s.program),
		ParseError:       s.parseErr,
		Presentation:     cloneExecutionPresentation(s.presentation),
		State:            s.state,
		StartPending:     s.startPending,
		OperationPending: s.operationPending,
		LastOperationErr: s.lastOperationErr,
		Controls: SessionExecutionControlState(s.id, s.presentation.RunID,
			s.parseErr == nil && len(s.program.Lines) > 0, s.startPending, s.awaitingRunState != 0, s.operationPending, s.state),
	}
}

func cloneExecutionProgram(program gcode.Program) gcode.Program {
	program.Lines = append([]gcode.Line(nil), program.Lines...)
	return program
}

func cloneExecutionPresentation(presentation DocumentExecutionPresentation) DocumentExecutionPresentation {
	presentation.lines = append([]gcode.Line(nil), presentation.lines...)
	return presentation
}

// Attach synchronously sends the current snapshot and returns an idempotent
// detach function. Callers must detach before their native UI object can be
// deleted.
func (s *ExecutionSession) Attach(observer func(ExecutionSessionSnapshot)) func() {
	if observer == nil {
		return func() {}
	}
	s.nextObserver++
	id := s.nextObserver
	if s.observers == nil {
		s.observers = make(map[uint64]func(ExecutionSessionSnapshot))
	}
	s.observers[id] = observer
	observer(s.Snapshot())
	removed := false
	return func() {
		if removed {
			return
		}
		removed = true
		delete(s.observers, id)
	}
}

func (s *ExecutionSession) notify() {
	for _, observer := range s.observers {
		// Program contains a slice. Give every observer its own snapshot so one
		// presentation consumer cannot mutate another consumer's view of the
		// retained executable program.
		observer(s.Snapshot())
	}
}

func (s *ExecutionSession) detachAll() {
	clear(s.observers)
}

// BeginStart reserves one exact frontend admission. The caller still acquires
// ProgramStartGate before launching the controller goroutine.
func (s *ExecutionSession) BeginStart() (uint64, bool) {
	controls := s.Snapshot().Controls
	if !controls.Run || s.startPending || s.awaitingRunState != 0 || s.operationPending {
		return 0, false
	}
	s.nextRequest++
	s.startRequest = s.nextRequest
	s.startPending = true
	s.lastOperationErr = nil
	s.notify()
	return s.startRequest, true
}

// FinishStart applies only the outstanding request. It deliberately leaves
// newer controller state intact through DocumentExecutionPresentation's
// admission reconciliation.
func (s *ExecutionSession) FinishStart(request uint64, runID app.RunID, err error) bool {
	if !s.startPending || request == 0 || request != s.startRequest {
		return false
	}
	s.startPending = false
	s.startRequest = 0
	s.presentation.ReconcileAdmission(runID, err)
	if err != nil {
		s.lastOperationErr = err
	} else if runID != 0 {
		// StartProgram has accepted a run, but the session must not expose a
		// second Run until the revisioned controller stream identifies it.
		s.awaitingRunState = runID
		if s.state.ProgramDocumentID == s.id.DocumentID && s.state.ProgramRunID == runID {
			s.awaitingRunState = 0
		}
	}
	s.notify()
	return true
}

// BeginAction serializes Pause, Resume, and Stop from every attached view.
func (s *ExecutionSession) BeginAction(runID app.RunID) bool {
	if s.operationPending || runID == 0 || runID != s.presentation.RunID || s.state.ProgramDocumentID != s.id.DocumentID || s.state.ProgramRunID != runID {
		return false
	}
	s.operationPending = true
	s.actionRunID = runID
	s.lastOperationErr = nil
	s.notify()
	return true
}

func (s *ExecutionSession) FinishAction(runID app.RunID, err error) bool {
	if !s.operationPending || runID == 0 || runID != s.actionRunID || runID != s.presentation.RunID {
		return false
	}
	s.operationPending = false
	s.actionRunID = 0
	if err != nil {
		s.lastOperationErr = err
		s.presentation.Error = err.Error()
	}
	s.notify()
	return true
}

func (s *ExecutionSession) ApplyState(state app.State) {
	s.state = state
	s.applyRunState(state)
	s.notify()
}

// applyRunState updates retained run presentation without changing s.state.
// Buffered admission state uses this path so an older run snapshot cannot
// roll back newer global readiness, connection, or emergency-stop state.
func (s *ExecutionSession) applyRunState(state app.State) {
	if state.ProgramDocumentID != s.id.DocumentID || state.ProgramRunID == 0 || state.ProgramRunID != s.presentation.RunID {
		return
	}
	s.presentation.ApplyState(state)
	if s.awaitingRunState == state.ProgramRunID {
		s.awaitingRunState = 0
	}
}

func (s *ExecutionSession) applyGlobalState(state app.State) {
	s.state = state
	s.notify()
}

func (s *ExecutionSession) ApplyEvent(event app.Event) {
	if event.Kind == app.EventError && event.ErrorDocumentID == s.id.DocumentID && event.ErrorRunID == s.presentation.RunID {
		s.presentation.ApplyEvent(event)
		s.notify()
	}
}

// SessionExecutionControlState applies the established document policy and
// then narrows active controls to the exact session RunID. DocumentID alone
// cannot identify a package step because several step sessions share the
// package's owning document identity.
func SessionExecutionControlState(id ExecutionSessionID, sessionRunID app.RunID, executable, startPending, awaitingRunState, operationPending bool, state app.State) DocumentExecutionControls {
	controls := DocumentExecutionControlState(id.DocumentID, executable, startPending, operationPending, state)
	if awaitingRunState {
		controls.Run = false
	}
	if state.ProgramDocumentID != id.DocumentID || state.ProgramRunID == 0 || state.ProgramRunID != sessionRunID {
		controls.Pause = false
		controls.Resume = false
		controls.Stop = false
	}
	return controls
}

// ExecutionSessions routes the one accepted controller event stream to
// sessions. It records a bounded pending update for a RunID observed before
// the matching asynchronous admission result, which is required for package
// steps sharing one owning DocumentID.
type ExecutionSessions struct {
	sessions       map[ExecutionSessionID]*ExecutionSession
	runOwners      map[app.RunID]ExecutionSessionID
	pendingStates  map[app.RunID]app.State
	pendingEvents  map[app.RunID][]app.Event
	stateRevision  app.StateRevision
	currentState   app.State
	maxPendingRuns int
}

func NewExecutionSessions(state app.State, revision app.StateRevision) *ExecutionSessions {
	return &ExecutionSessions{
		sessions:       make(map[ExecutionSessionID]*ExecutionSession),
		runOwners:      make(map[app.RunID]ExecutionSessionID),
		pendingStates:  make(map[app.RunID]app.State),
		pendingEvents:  make(map[app.RunID][]app.Event),
		stateRevision:  revision,
		currentState:   state,
		maxPendingRuns: 32,
	}
}

func (m *ExecutionSessions) Add(id ExecutionSessionID, source string, program gcode.Program, parseErr error) (*ExecutionSession, error) {
	if _, exists := m.sessions[id]; exists {
		return nil, fmt.Errorf("execution session already exists")
	}
	s, err := NewExecutionSession(id, source, program, parseErr, m.currentState)
	if err != nil {
		return nil, err
	}
	m.sessions[id] = s
	return s, nil
}

func (m *ExecutionSessions) Session(id ExecutionSessionID) (*ExecutionSession, bool) {
	s, ok := m.sessions[id]
	return s, ok
}

// BeginStart is the owner-level admission check used by application command
// callbacks. Standalone sessions retain their existing behavior. For a future
// package, sibling steps sharing the owner DocumentID cannot speculatively
// start while another step's admission has not reached controller state.
func (m *ExecutionSessions) BeginStart(id ExecutionSessionID) (uint64, bool) {
	s, ok := m.sessions[id]
	if !ok {
		return 0, false
	}
	if !id.Standalone() {
		for otherID, other := range m.sessions {
			if otherID == id || otherID.DocumentID != id.DocumentID || otherID.Standalone() {
				continue
			}
			if other.startPending || other.awaitingRunState != 0 {
				return 0, false
			}
		}
	}
	return s.BeginStart()
}

func (m *ExecutionSessions) Remove(id ExecutionSessionID) {
	s, ok := m.sessions[id]
	if !ok {
		return
	}
	if runID := s.presentation.RunID; runID != 0 && m.runOwners[runID] == id {
		delete(m.runOwners, runID)
	}
	s.detachAll()
	delete(m.sessions, id)
}

func (m *ExecutionSessions) AssociateRun(id ExecutionSessionID, request uint64, runID app.RunID, err error) bool {
	s, ok := m.sessions[id]
	if !ok {
		return false
	}
	oldRunID := s.presentation.RunID
	if !s.FinishStart(request, runID, err) {
		return false
	}
	if err != nil || runID == 0 {
		return true
	}
	if oldRunID != 0 && oldRunID != runID {
		delete(m.runOwners, oldRunID)
	}
	m.runOwners[runID] = id
	if state, pending := m.pendingStates[runID]; pending {
		delete(m.pendingStates, runID)
		// This state may be older than m.currentState. It belongs to this exact
		// run's history but must not replace current machine-control policy.
		s.applyRunState(state)
		s.notify()
	}
	for _, event := range m.pendingEvents[runID] {
		s.ApplyEvent(event)
	}
	delete(m.pendingEvents, runID)
	return true
}

func (m *ExecutionSessions) ApplyEvent(event app.Event) {
	if event.StateRevision > m.stateRevision {
		m.stateRevision = event.StateRevision
		m.currentState = event.State
		for _, s := range m.sessions {
			s.applyGlobalState(event.State)
		}
		runID := event.State.ProgramRunID
		if owner, ok := m.runOwners[runID]; ok {
			if s, exists := m.sessions[owner]; exists {
				s.applyRunState(event.State)
				s.notify()
			}
		} else if m.awaitingAdmission(event.State.ProgramDocumentID) && runID != 0 {
			m.rememberState(runID, event.State)
		}
	}
	if event.Kind != app.EventError || event.ErrorRunID == 0 {
		return
	}
	if owner, ok := m.runOwners[event.ErrorRunID]; ok {
		if s, exists := m.sessions[owner]; exists {
			s.ApplyEvent(event)
		}
		return
	}
	if m.awaitingAdmission(event.ErrorDocumentID) {
		m.rememberEvent(event.ErrorRunID, event)
	}
}

func (m *ExecutionSessions) awaitingAdmission(documentID app.DocumentID) bool {
	for id, s := range m.sessions {
		if id.DocumentID == documentID && s.startPending {
			return true
		}
	}
	return false
}

func (m *ExecutionSessions) rememberState(runID app.RunID, state app.State) {
	if _, exists := m.pendingStates[runID]; !exists && len(m.pendingStates) >= m.maxPendingRuns {
		return
	}
	m.pendingStates[runID] = state
}

func (m *ExecutionSessions) rememberEvent(runID app.RunID, event app.Event) {
	if _, exists := m.pendingEvents[runID]; !exists && len(m.pendingEvents) >= m.maxPendingRuns {
		return
	}
	m.pendingEvents[runID] = append(m.pendingEvents[runID], event)
}
