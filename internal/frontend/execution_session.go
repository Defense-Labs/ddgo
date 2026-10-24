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
	ID                ExecutionSessionID
	Source            string
	Program           gcode.Program
	ParseError        error
	Presentation      DocumentExecutionPresentation
	State             app.State
	StartPending      bool
	AwaitingRunState  bool
	OwnerStartBlocked bool
	OperationPending  bool
	StartErr          error
	LastOperationErr  error
	Controls          DocumentExecutionControls
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

	startPending      bool
	awaitingRunState  app.RunID
	ownerStartBlocked bool
	operationPending  bool
	startErr          error
	lastOperationErr  error
	nextRequest       uint64
	startRequest      uint64
	actionRunID       app.RunID

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
		ID:                s.id,
		Source:            s.source,
		Program:           cloneExecutionProgram(s.program),
		ParseError:        s.parseErr,
		Presentation:      cloneExecutionPresentation(s.presentation),
		State:             s.state,
		StartPending:      s.startPending,
		AwaitingRunState:  s.awaitingRunState != 0,
		OwnerStartBlocked: s.ownerStartBlocked,
		OperationPending:  s.operationPending,
		StartErr:          s.startErr,
		LastOperationErr:  s.lastOperationErr,
		Controls: SessionExecutionControlState(s.id, s.presentation.RunID,
			s.parseErr == nil && len(s.program.Lines) > 0, s.startPending, s.awaitingRunState != 0, s.ownerStartBlocked, s.operationPending, s.state),
	}
}

// PackageExecutionPhase describes the package-level attempt independently of
// controller RunID history. A rejected admission has no RunID, but remains an
// important operator-visible execution attempt.
type PackageExecutionPhase uint8

const (
	PackageExecutionNone PackageExecutionPhase = iota
	PackageExecutionStartPending
	PackageExecutionAwaitingState
	PackageExecutionActive
	PackageExecutionStartRejected
	PackageExecutionHistorical
)

// PackageExecutionPresentation is intentionally lighter than an
// ExecutionSessionSnapshot: the package strip never needs a mutable copy of
// the G-code program. It combines exact session state with a monotonically
// ordered package Start attempt.
type PackageExecutionPresentation struct {
	ID               ExecutionSessionID
	Presentation     DocumentExecutionPresentation
	State            app.State
	OperationPending bool
	LastOperationErr error
	Controls         DocumentExecutionControls
	Phase            PackageExecutionPhase
	AttemptSequence  uint64
	AttemptRunID     app.RunID
	AttemptErr       error
}

func (s *ExecutionSession) packagePresentation() PackageExecutionPresentation {
	return PackageExecutionPresentation{
		ID:               s.id,
		Presentation:     cloneExecutionPresentation(s.presentation),
		State:            s.state,
		OperationPending: s.operationPending,
		LastOperationErr: s.lastOperationErr,
		Controls: SessionExecutionControlState(s.id, s.presentation.RunID,
			s.parseErr == nil && len(s.program.Lines) > 0, s.startPending, s.awaitingRunState != 0, s.ownerStartBlocked, s.operationPending, s.state),
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
	s.startErr = nil
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
		s.startErr = err
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
func SessionExecutionControlState(id ExecutionSessionID, sessionRunID app.RunID, executable, startPending, awaitingRunState, ownerStartBlocked, operationPending bool, state app.State) DocumentExecutionControls {
	controls := DocumentExecutionControlState(id.DocumentID, executable, startPending, operationPending, state)
	if awaitingRunState || ownerStartBlocked {
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

	nextPackageAttempt  uint64
	packageAttempts     map[app.DocumentID]packageExecutionAttempt
	nextPackageObserver uint64
	packageObservers    map[app.DocumentID]map[uint64]func(PackageExecutionPresentation)
	lastActivePackage   app.DocumentID
}

type packageExecutionAttempt struct {
	sequence uint64
	id       ExecutionSessionID
	request  uint64
	runID    app.RunID
	err      error
	phase    PackageExecutionPhase
}

func NewExecutionSessions(state app.State, revision app.StateRevision) *ExecutionSessions {
	return &ExecutionSessions{
		sessions:         make(map[ExecutionSessionID]*ExecutionSession),
		runOwners:        make(map[app.RunID]ExecutionSessionID),
		pendingStates:    make(map[app.RunID]app.State),
		pendingEvents:    make(map[app.RunID][]app.Event),
		stateRevision:    revision,
		currentState:     state,
		maxPendingRuns:   32,
		packageAttempts:  make(map[app.DocumentID]packageExecutionAttempt),
		packageObservers: make(map[app.DocumentID]map[uint64]func(PackageExecutionPresentation)),
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
	m.refreshPackageAdmission(id.DocumentID, false)
	return s, nil
}

func (m *ExecutionSessions) Session(id ExecutionSessionID) (*ExecutionSession, bool) {
	s, ok := m.sessions[id]
	return s, ok
}

// AttachPackage observes one package execution projection. Unlike attaching
// to every visited step, this produces one package-strip callback per relevant
// owner transition. It is UI-thread owned, synchronous, and detachable before
// the owning native package window is destroyed.
func (m *ExecutionSessions) AttachPackage(documentID app.DocumentID, observer func(PackageExecutionPresentation)) func() {
	if documentID == 0 || observer == nil {
		return func() {}
	}
	m.nextPackageObserver++
	id := m.nextPackageObserver
	if id == 0 {
		m.nextPackageObserver++
		id = m.nextPackageObserver
	}
	if m.packageObservers[documentID] == nil {
		m.packageObservers[documentID] = make(map[uint64]func(PackageExecutionPresentation))
	}
	m.packageObservers[documentID][id] = observer
	observer(m.packageExecution(documentID))
	removed := false
	return func() {
		if removed {
			return
		}
		removed = true
		delete(m.packageObservers[documentID], id)
		if len(m.packageObservers[documentID]) == 0 {
			delete(m.packageObservers, documentID)
		}
	}
}

func (m *ExecutionSessions) notifyPackage(documentID app.DocumentID) {
	observers := m.packageObservers[documentID]
	if len(observers) == 0 {
		return
	}
	presentation := m.packageExecution(documentID)
	for _, observer := range observers {
		observer(presentation)
	}
}

// PackageExecution returns the package step which is pending admission,
// currently owns the controller run, or most recently ran, in that order.
// It is deliberately a manager projection rather than a window concern:
// package selection is navigation state and must never decide execution
// ownership. A package has at most one unresolved admission by BeginStart's
// owner-level rule, and RunIDs provide a deterministic ordering for retained
// historical step presentations.
func (m *ExecutionSessions) PackageExecution(documentID app.DocumentID) (PackageExecutionPresentation, bool) {
	presentation := m.packageExecution(documentID)
	return presentation, presentation.Phase != PackageExecutionNone
}

func (m *ExecutionSessions) packageExecution(documentID app.DocumentID) PackageExecutionPresentation {
	var active *ExecutionSession
	var latest *ExecutionSession
	for id, session := range m.sessions {
		if id.DocumentID != documentID || id.Standalone() {
			continue
		}
		if session.presentation.RunID != 0 && m.currentState.ProgramDocumentID == documentID &&
			m.currentState.ProgramRunID == session.presentation.RunID && m.currentState.ProgramStatus.IsActive() {
			active = session
			continue
		}
		if session.presentation.RunID != 0 && (latest == nil || session.presentation.RunID > latest.presentation.RunID) {
			latest = session
		}
	}
	if active != nil {
		out := active.packagePresentation()
		out.Phase = PackageExecutionActive
		return out
	}
	attempt, hasAttempt := m.packageAttempts[documentID]
	if hasAttempt {
		if session, exists := m.sessions[attempt.id]; exists {
			out := session.packagePresentation()
			out.Phase, out.AttemptSequence, out.AttemptRunID, out.AttemptErr = attempt.phase, attempt.sequence, attempt.runID, attempt.err
			if attempt.phase == PackageExecutionStartPending || attempt.phase == PackageExecutionAwaitingState || attempt.phase == PackageExecutionStartRejected {
				return out
			}
		}
	}
	if latest != nil {
		out := latest.packagePresentation()
		out.Phase = PackageExecutionHistorical
		if hasAttempt && attempt.id == latest.id {
			out.AttemptSequence, out.AttemptRunID = attempt.sequence, attempt.runID
		}
		return out
	}
	return PackageExecutionPresentation{}
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
	if m.packageAdmissionBlocked(id) {
		return 0, false
	}
	request, accepted := s.BeginStart()
	if accepted {
		if !id.Standalone() {
			m.nextPackageAttempt++
			m.packageAttempts[id.DocumentID] = packageExecutionAttempt{sequence: m.nextPackageAttempt, id: id, request: request, phase: PackageExecutionStartPending}
		}
		// The session's own transition notifies its views. Refreshing the owner
		// immediately updates every sibling before any controller state arrives.
		m.refreshPackageAdmission(id.DocumentID, false)
		m.notifyPackage(id.DocumentID)
	}
	return request, accepted
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
	if attempt, exists := m.packageAttempts[id.DocumentID]; exists && attempt.id == id {
		delete(m.packageAttempts, id.DocumentID)
	}
	m.refreshPackageAdmission(id.DocumentID, false)
	m.notifyPackage(id.DocumentID)
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
		m.recordPackageAttempt(id, request, runID, err, PackageExecutionStartRejected)
		m.refreshPackageAdmission(id.DocumentID, true)
		m.notifyPackage(id.DocumentID)
		return true
	}
	if oldRunID != 0 && oldRunID != runID {
		delete(m.runOwners, oldRunID)
	}
	m.runOwners[runID] = id
	m.recordPackageAttempt(id, request, runID, nil, PackageExecutionAwaitingState)
	if state, pending := m.pendingStates[runID]; pending {
		delete(m.pendingStates, runID)
		// This state may be older than m.currentState. It belongs to this exact
		// run's history but must not replace current machine-control policy.
		s.applyRunState(state)
		if !state.ProgramStatus.IsActive() {
			m.recordPackageAttempt(id, request, runID, nil, PackageExecutionHistorical)
		}
		s.notify()
	}
	for _, event := range m.pendingEvents[runID] {
		s.ApplyEvent(event)
	}
	delete(m.pendingEvents, runID)
	m.refreshPackageAdmission(id.DocumentID, true)
	m.notifyPackage(id.DocumentID)
	return true
}

func (m *ExecutionSessions) ApplyEvent(event app.Event) {
	if event.StateRevision > m.stateRevision {
		previousActive := m.lastActivePackage
		nextActive := app.DocumentID(0)
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
				if attempt, exists := m.packageAttempts[owner.DocumentID]; exists && attempt.id == owner && attempt.runID == runID && !event.State.ProgramStatus.IsActive() {
					attempt.phase = PackageExecutionHistorical
					m.packageAttempts[owner.DocumentID] = attempt
				}
				m.refreshPackageAdmission(owner.DocumentID, false)
				if !owner.Standalone() && event.State.ProgramStatus.IsActive() {
					nextActive = owner.DocumentID
				}
			}
		} else if m.awaitingAdmission(event.State.ProgramDocumentID) && runID != 0 {
			m.rememberState(runID, event.State)
		}
		m.lastActivePackage = nextActive
		if nextActive != 0 {
			m.notifyPackage(nextActive)
		}
		if previousActive != 0 && previousActive != nextActive {
			m.notifyPackage(previousActive)
		}
	}
	if event.Kind != app.EventError || event.ErrorRunID == 0 {
		return
	}
	if owner, ok := m.runOwners[event.ErrorRunID]; ok {
		if s, exists := m.sessions[owner]; exists {
			s.ApplyEvent(event)
			if !owner.Standalone() {
				m.notifyPackage(owner.DocumentID)
			}
		}
		return
	}
	if m.awaitingAdmission(event.ErrorDocumentID) {
		m.rememberEvent(event.ErrorRunID, event)
	}
}

func (m *ExecutionSessions) recordPackageAttempt(id ExecutionSessionID, request uint64, runID app.RunID, err error, phase PackageExecutionPhase) {
	if id.Standalone() {
		return
	}
	attempt, exists := m.packageAttempts[id.DocumentID]
	if !exists || attempt.id != id || attempt.request != request {
		return
	}
	attempt.runID, attempt.err, attempt.phase = runID, err, phase
	m.packageAttempts[id.DocumentID] = attempt
}

// BeginAction and FinishAction keep local package-strip controls synchronized
// without requiring one package observer per retained step.
func (m *ExecutionSessions) BeginAction(id ExecutionSessionID, runID app.RunID) bool {
	session, ok := m.sessions[id]
	if !ok || !session.BeginAction(runID) {
		return false
	}
	m.notifyPackage(id.DocumentID)
	return true
}

func (m *ExecutionSessions) FinishAction(id ExecutionSessionID, runID app.RunID, err error) bool {
	session, ok := m.sessions[id]
	if !ok || !session.FinishAction(runID, err) {
		return false
	}
	m.notifyPackage(id.DocumentID)
	return true
}

// packageAdmissionBlocked is the sole package-owner admission rule. Only
// non-standalone sessions with the same owning package DocumentID are
// siblings; standalone documents and other packages remain independent.
func (m *ExecutionSessions) packageAdmissionBlocked(id ExecutionSessionID) bool {
	if id.Standalone() {
		return false
	}
	for otherID, other := range m.sessions {
		if otherID == id || otherID.DocumentID != id.DocumentID || otherID.Standalone() {
			continue
		}
		if other.startPending || other.awaitingRunState != 0 {
			return true
		}
	}
	return false
}

// refreshPackageAdmission projects the manager's admission rule into each
// package-step snapshot. It notifies only sessions whose rendered Run
// availability can change; normal controller-state notifications remain
// separate and revision ordered.
func (m *ExecutionSessions) refreshPackageAdmission(documentID app.DocumentID, force bool) {
	for id, session := range m.sessions {
		if id.DocumentID != documentID || id.Standalone() {
			continue
		}
		blocked := m.packageAdmissionBlocked(id)
		if session.ownerStartBlocked != blocked {
			session.ownerStartBlocked = blocked
			session.notify()
		} else if force {
			session.notify()
		}
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
