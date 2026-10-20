//go:build miqt

package miqt

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/frontend"
	"github.com/ianbruene/ddgo/internal/gcode"
	qt "github.com/mappu/miqt/qt6"
)

// Application owns the Qt lifetime, top-level windows, and the single UI-side
// subscription to controller events.
type Application struct {
	controller *app.Controller

	pollTimer  *qt.QTimer
	dispatcher *frontend.ViewDispatcher
	windows    *frontend.WindowManager
	documents  *frontend.DocumentRegistry
	sessions   *frontend.ExecutionSessions
	mainWindow *MainWindow

	pendingDocumentCloses  map[app.DocumentID]app.RunID
	approvedDocumentCloses frontend.DocumentCloseApprovals
	documentCloseResults   chan documentCloseResult
	pendingDocumentStarts  map[frontend.ExecutionSessionID]documentStartRequest
	documentStartResults   chan documentStartResult
	pendingDocumentActions map[frontend.ExecutionSessionID]app.RunID
	documentActionResults  chan documentActionResult

	shutdown            frontend.ShutdownCoordinator
	shutdownStopResults chan shutdownStopResult
	mainCloseIssued     bool
	programStarts       frontend.ProgramStartGate
}

type documentCloseResult struct {
	documentID app.DocumentID
	runID      app.RunID
	err        error
}

type documentStartRequest struct{ id uint64 }
type documentStartResult struct {
	documentID app.DocumentID
	sessionID  frontend.ExecutionSessionID
	requestID  uint64
	runID      app.RunID
	err        error
}
type documentActionResult struct {
	documentID app.DocumentID
	sessionID  frontend.ExecutionSessionID
	runID      app.RunID
	err        error
}
type shutdownStopResult struct {
	attempt frontend.ShutdownAttemptID
	target  frontend.ShutdownStopTarget
	err     error
}

func newApplication(controller *app.Controller) *Application {
	return &Application{controller: controller}
}

// Run creates and runs the UI application.
func Run(controller *app.Controller) error { return newApplication(controller).Run() }

func (a *Application) Run() error {
	qt.NewQApplication(os.Args)

	state, revision := a.controller.SnapshotWithRevision()
	a.dispatcher = frontend.NewViewDispatcher(state, revision)
	a.windows = frontend.NewWindowManager(a.dispatcher)
	a.documents = frontend.NewDocumentRegistry(a.windows)
	a.documents.SetOnClose(a.documentClosed)
	a.sessions = frontend.NewExecutionSessions(state, revision)
	a.pendingDocumentCloses = make(map[app.DocumentID]app.RunID)
	a.approvedDocumentCloses = make(frontend.DocumentCloseApprovals)
	a.documentCloseResults = make(chan documentCloseResult, 16)
	a.pendingDocumentStarts = make(map[frontend.ExecutionSessionID]documentStartRequest)
	a.documentStartResults = make(chan documentStartResult, 16)
	a.pendingDocumentActions = make(map[frontend.ExecutionSessionID]app.RunID)
	a.documentActionResults = make(chan documentActionResult, 16)
	a.shutdownStopResults = make(chan shutdownStopResult, 2)

	mainWindow := newMainWindow(a.controller, func() { a.openGCodeFile() }, a.showExecutionDocument, a.canShowExecutionDocument)
	a.mainWindow = mainWindow
	mainWindow.SetOnCloseRequest(a.requestMainClose)
	a.windows.Add(mainWindow)
	mainWindow.show()

	a.pollTimer = qt.NewQTimer()
	a.pollTimer.OnTimeout(func() { a.drainControllerEvents() })
	a.pollTimer.Start(50)

	monitorContext, stopMonitor := context.WithCancel(context.Background())
	a.controller.StartPortMonitoring(monitorContext)
	qt.QApplication_Exec()
	a.pollTimer.Stop()
	stopMonitor()
	a.controller.StopPortMonitoring()
	return nil
}

func (a *Application) openGCodeFile() {
	path := chooseProgramFile(nil)
	if path == "" {
		return
	}
	entry, opened, err := a.documents.OpenFile(path, gcode.LoadDocument, func(id app.DocumentID, document gcode.Document) frontend.DocumentWindow {
		window := newGCodeWindow(document, func() { a.openGCodeFile() })
		window.SetOnCloseRequest(func() bool { return a.requestDocumentClose(id) })
		return window
	})
	if err != nil {
		qt.QMessageBox_Critical(nil, "Unable to Open File", fmt.Sprintf("Could not open %s:\n%v", path, err))
		return
	}
	if opened {
		window, ok := entry.Window.(*GCodeWindow)
		if !ok {
			return
		}
		a.configureDocumentExecution(entry, window)
		window.Focus()
	}
	a.refreshExecutionFocusAvailability()
}

func (a *Application) configureDocumentExecution(entry frontend.DocumentEntry, window *GCodeWindow) {
	program, err := gcode.ProgramFromDocument(entry.Document)
	sessionID := frontend.StandaloneExecutionSessionID(entry.ID)
	session, sessionErr := a.sessions.Add(sessionID, entry.Document.Source, program, err)
	if sessionErr != nil {
		// A duplicate can only arise from an interrupted UI configuration path.
		// Reuse the existing authoritative session rather than creating a second
		// execution history for the same document identity.
		var exists bool
		session, exists = a.sessions.Session(sessionID)
		if !exists {
			return
		}
	}
	window.SetExecutionSession(session,
		func() { a.startDocumentProgram(sessionID) },
		func() { a.documentProgramAction(sessionID, "pause") },
		func() { a.documentProgramAction(sessionID, "resume") },
		func() { a.documentProgramAction(sessionID, "stop") },
	)
}

func (a *Application) drainControllerEvents() {
	for {
		select {
		case event := <-a.controller.Events():
			a.dispatcher.Dispatch(event)
			a.sessions.ApplyEvent(event)
		default:
			a.drainDocumentStartResults()
			a.drainDocumentActionResults()
			a.drainDocumentCloseResults()
			a.drainShutdownStopResults()
			a.progressShutdown()
			a.syncProgramStartGate()
			return
		}
	}
}

func (a *Application) startDocumentProgram(sessionID frontend.ExecutionSessionID) {
	id := sessionID.DocumentID
	if _, pending := a.pendingDocumentStarts[sessionID]; pending || a.documents.ClosePending(id) {
		return
	}
	if _, ok := a.documents.Entry(id); !ok {
		return
	}
	session, ok := a.sessions.Session(sessionID)
	if !ok {
		return
	}
	admission, ok := a.programStarts.Acquire()
	if !ok {
		return
	}
	requestID, ok := a.sessions.BeginStart(sessionID)
	if !ok {
		admission.Release()
		return
	}
	request := documentStartRequest{id: requestID}
	a.pendingDocumentStarts[sessionID] = request
	snapshot := session.Snapshot()
	program, parseErr := snapshot.Program, snapshot.ParseError
	go func() {
		defer admission.Release()
		if parseErr != nil {
			a.documentStartResults <- documentStartResult{documentID: id, sessionID: sessionID, requestID: request.id, err: parseErr}
			return
		}
		runID, err := a.controller.StartDocumentProgram(context.Background(), id, program)
		a.documentStartResults <- documentStartResult{documentID: id, sessionID: sessionID, requestID: request.id, runID: runID, err: err}
	}()
}

func (a *Application) drainDocumentStartResults() {
	for {
		select {
		case result := <-a.documentStartResults:
			request, pending := a.pendingDocumentStarts[result.sessionID]
			if !pending || request.id != result.requestID {
				continue
			}
			delete(a.pendingDocumentStarts, result.sessionID)
			entry, open := a.documents.Entry(result.documentID)
			if !open {
				continue
			}
			window, ok := entry.Window.(*GCodeWindow)
			if !ok {
				continue
			}
			if !a.sessions.AssociateRun(result.sessionID, result.requestID, result.runID, result.err) {
				continue
			}
			if !a.documents.ClosePending(result.documentID) {
				continue
			}
			a.documents.ClearClosePending(result.documentID)
			state := a.controller.Snapshot()
			if result.err != nil {
				if !state.ProgramStatus.IsActive() {
					_ = window.Close()
				}
				continue
			}
			// A successful admission is never auto-stopped. Reissue the close
			// request on the UI thread so the normal Stop-and-Close confirmation
			// governs this exact run.
			if state.ProgramDocumentID == result.documentID && state.ProgramRunID == result.runID && state.ProgramStatus.IsActive() {
				_ = window.Close()
			}
		default:
			return
		}
	}
}

func (a *Application) documentProgramAction(sessionID frontend.ExecutionSessionID, action string) {
	id := sessionID.DocumentID
	if _, pending := a.pendingDocumentActions[sessionID]; pending {
		return
	}
	state := a.controller.Snapshot()
	if state.ProgramDocumentID != id || state.ProgramRunID == 0 {
		return
	}
	runID := state.ProgramRunID
	if _, ok := a.documents.Entry(id); !ok {
		return
	}
	session, ok := a.sessions.Session(sessionID)
	if !ok || !session.BeginAction(runID) {
		return
	}
	a.pendingDocumentActions[sessionID] = runID
	go func() {
		var err error
		switch action {
		case "pause":
			err = a.controller.PauseProgramRun(context.Background(), id, runID)
		case "resume":
			err = a.controller.ResumeProgramRun(context.Background(), id, runID)
		case "stop":
			err = a.controller.StopProgramRun(context.Background(), id, runID)
		}
		a.documentActionResults <- documentActionResult{documentID: id, sessionID: sessionID, runID: runID, err: err}
	}()
}

func (a *Application) drainDocumentActionResults() {
	for {
		select {
		case result := <-a.documentActionResults:
			runID, pending := a.pendingDocumentActions[result.sessionID]
			if !pending || runID != result.runID {
				continue
			}
			delete(a.pendingDocumentActions, result.sessionID)
			if _, ok := a.documents.Entry(result.documentID); ok {
				if session, exists := a.sessions.Session(result.sessionID); exists {
					session.FinishAction(result.runID, result.err)
				}
			}
		default:
			return
		}
	}
}

func (a *Application) syncProgramStartGate() {
	if a.mainCloseIssued || a.shutdown.Phase() == frontend.ShutdownFinished {
		a.programStarts.Finalize()
		return
	}
	if a.shutdown.Active() {
		a.programStarts.Freeze()
		return
	}
	a.programStarts.Reopen()
}

// hasPendingSessionStart keeps document close safety tied to document
// ownership rather than to a particular view. It already supports the future
// case where one package document owns several step sessions.
func (a *Application) hasPendingSessionStart(documentID app.DocumentID) bool {
	for sessionID := range a.pendingDocumentStarts {
		if sessionID.DocumentID == documentID {
			return true
		}
	}
	return false
}

// requestDocumentClose governs independent document closure. An application
// shutdown is the sole closer while its coordinator is active, preventing a
// second document Stop request or an accidental partial shutdown.
func (a *Application) requestDocumentClose(id app.DocumentID) bool {
	defer a.syncProgramStartGate()
	if a.shutdown.Active() {
		attempt := a.shutdown.Attempt()
		state := a.controller.Snapshot()
		if !a.shutdown.AllowsDocumentClose(attempt, id) {
			return false
		}
		// Selection is necessary but not sufficient: controller execution may
		// have changed after shutdown preflight. Never translate an older close
		// request into an unscoped Stop of a newly admitted run.
		if !a.shutdown.CanCloseSelectedDocument(attempt, id, state) {
			a.shutdown.DocumentCloseRejected(attempt, id)
			a.warnMain("Shutdown Cancelled", "Controller execution or machine recovery became active before the document could close.")
			return false
		}
		return true
	}
	if _, approved := a.approvedDocumentCloses[id]; approved {
		state := a.controller.Snapshot()
		if a.approvedDocumentCloses.InvalidateSuperseded(a.documents, id, state) {
			// A new run for this document supersedes an earlier Stop-and-Close
			// approval and requires a fresh explicit close request.
			return false
		}
		return true
	}
	entry, ok := a.documents.Entry(id)
	if !ok {
		return true
	}
	if a.hasPendingSessionStart(id) {
		// The UI accepted Run before its controller call returned. Keep the
		// document alive until that exact admission resolves.
		a.documents.MarkClosePending(id)
		return false
	}
	if a.documents.ClosePending(id) {
		return false
	}
	state := a.controller.Snapshot()
	if !state.ProgramStatus.IsActive() || state.ProgramDocumentID != id {
		return true
	}
	if !confirmStopAndClose(documentParent(entry)) || !a.documents.MarkClosePending(id) {
		return false
	}
	runID := state.ProgramRunID
	a.pendingDocumentCloses[id] = runID
	go func() {
		err := a.controller.StopProgramRun(context.Background(), id, runID)
		a.documentCloseResults <- documentCloseResult{documentID: id, runID: runID, err: err}
	}()
	return false
}

func (a *Application) drainDocumentCloseResults() {
	for {
		select {
		case result := <-a.documentCloseResults:
			expectedRun, pending := a.pendingDocumentCloses[result.documentID]
			if !pending || expectedRun != result.runID {
				continue
			}
			delete(a.pendingDocumentCloses, result.documentID)
			entry, stillOpen := a.documents.Entry(result.documentID)
			state := a.controller.Snapshot()
			disposition := frontend.ClassifyDocumentStopResult(result.err, stillOpen, result.documentID, result.runID, state, a.shutdown.Active())
			if disposition == frontend.DocumentStopInvalid {
				a.documents.ClearClosePending(result.documentID)
				if stillOpen {
					a.warnDocumentStopFailure(entry, result.err)
				}
				if a.shutdown.Active() {
					a.shutdown.Cancel(a.shutdown.Attempt())
					a.warnMain("Shutdown Cancelled", "An active document stop did not reach a verified stopped state. DDGo remains open for recovery.")
				}
				continue
			}
			if disposition == frontend.DocumentStopTransferToShutdown {
				// Shutdown owns native close selection. This independently accepted
				// Stop remains valid, but it may not borrow shutdown authorization.
				a.documents.ClearClosePending(result.documentID)
				delete(a.approvedDocumentCloses, result.documentID)
				continue
			}
			a.approvedDocumentCloses[result.documentID] = result.runID
			if !entry.Window.Close() {
				delete(a.approvedDocumentCloses, result.documentID)
				a.documents.ClearClosePending(result.documentID)
				qt.QMessageBox_Warning(documentParent(entry), "Document Remains Open", "The program stopped safely, but the document window did not close. You may try closing it again.")
			}
		default:
			return
		}
	}
}

func (a *Application) documentClosed(entry frontend.DocumentEntry) {
	delete(a.pendingDocumentCloses, entry.ID)
	delete(a.pendingDocumentStarts, frontend.StandaloneExecutionSessionID(entry.ID))
	delete(a.pendingDocumentActions, frontend.StandaloneExecutionSessionID(entry.ID))
	delete(a.approvedDocumentCloses, entry.ID)
	if a.sessions != nil {
		a.sessions.Remove(frontend.StandaloneExecutionSessionID(entry.ID))
	}
	// Do not close the main window here: this callback runs inside native
	// document destruction. The next UI timer turn advances the coordinator.
	if a.shutdown.Active() {
		a.shutdown.DocumentClosed(a.shutdown.Attempt(), entry.ID)
	}
	a.refreshExecutionFocusAvailability()
}

func (a *Application) refreshExecutionFocusAvailability() {
	if a.mainWindow != nil {
		a.mainWindow.RefreshExecutionFocusAvailability()
	}
}

func (a *Application) canShowExecutionDocument(documentID app.DocumentID, runID app.RunID) bool {
	if a.documents == nil {
		return false
	}
	state := a.controller.Snapshot()
	_, open := a.documents.Entry(documentID)
	return frontend.CanFocusExecutionSummary(state, documentID, runID, open)
}

func (a *Application) showExecutionDocument(documentID app.DocumentID, runID app.RunID) {
	if !a.canShowExecutionDocument(documentID, runID) {
		a.refreshExecutionFocusAvailability()
		return
	}
	entry, open := a.documents.Entry(documentID)
	if !open {
		a.refreshExecutionFocusAvailability()
		return
	}
	entry.Window.Focus()
	a.refreshExecutionFocusAvailability()
}

func (a *Application) requestMainClose() bool {
	defer a.syncProgramStartGate()
	if a.mainCloseIssued {
		return true
	}
	if a.shutdown.Active() {
		return false
	}
	state := a.controller.Snapshot()
	if state.MachineReadiness != app.MachineReadinessNormal {
		a.warnMain("Machine Recovery Required", "DDGo has not verified the controller's safe condition. Complete recovery before exiting.")
		return false
	}

	documents := a.documents.Entries()
	documentIDs := make([]app.DocumentID, 0, len(documents))
	for _, entry := range documents {
		documentIDs = append(documentIDs, entry.ID)
	}
	attempt, ok := a.shutdown.Begin(documentIDs)
	if !ok {
		return false
	}
	// Freeze before the confirmation dialog can return control to Qt. A Run
	// click already accepted by the UI is now visible through Outstanding(),
	// even if its controller goroutine has not started yet.
	a.programStarts.Freeze()
	if !confirmApplicationClose(a.mainParent(), len(documentIDs)) {
		a.shutdown.ConfirmApplication(attempt, false)
		return false
	}
	a.shutdown.ConfirmApplication(attempt, true)
	// Closing is always progressed on a later timer turn, never recursively
	// from this native main-window close event.
	return false
}

func (a *Application) startShutdownStop(attempt frontend.ShutdownAttemptID, target frontend.ShutdownStopTarget) {
	go func() {
		err := a.controller.StopProgramRun(context.Background(), target.DocumentID, target.RunID)
		a.shutdownStopResults <- shutdownStopResult{attempt: attempt, target: target, err: err}
	}()
}

func (a *Application) drainShutdownStopResults() {
	for {
		select {
		case result := <-a.shutdownStopResults:
			if result.attempt != a.shutdown.Attempt() || a.shutdown.Phase() != frontend.ShutdownWaitingForStop {
				continue
			}
			if errors.Is(result.err, app.ErrStopInProgress) {
				a.shutdown.StopAlreadyInProgress(result.attempt, result.target)
				continue
			}
			state := a.controller.Snapshot()
			verified := result.err == nil && state.ProgramStatus == app.ProgramStopped && state.MachineReadiness == app.MachineReadinessNormal &&
				state.ProgramDocumentID == result.target.DocumentID && state.ProgramRunID == result.target.RunID
			if !a.shutdown.StopResult(result.attempt, result.target, verified) {
				a.warnMain("Program Stop Failed", stopFailureText(result.err))
			}
		default:
			return
		}
	}
}

func (a *Application) progressShutdown() {
	defer a.syncProgramStartGate()
	if !a.shutdown.Active() || a.mainCloseIssued {
		return
	}
	attempt := a.shutdown.Attempt()
	if a.shutdown.Phase() == frontend.ShutdownWaitingForRunAdmission {
		if !a.programStarts.Quiescent() {
			return
		}
		// StartProgram updates controller state before its goroutine releases
		// the gate admission. Read only after observing quiescence so this
		// snapshot cannot predate the last accepted start's controller result.
		state := a.controller.Snapshot()
		if phase := a.shutdown.ResolveRunAdmission(attempt, state); phase == frontend.ShutdownIdle {
			a.warnMain("Shutdown Cancelled", "Machine recovery became necessary while program start admission was settling.")
		}
		return
	}
	if a.shutdown.Phase() == frontend.ShutdownWaitingForStopConfirmation {
		if !confirmStopAndClose(a.mainParent()) {
			a.shutdown.ConfirmStop(attempt, false)
			return
		}
		if target, ok := a.shutdown.ConfirmStop(attempt, true); ok {
			a.startShutdownStop(attempt, target)
		}
		return
	}
	state := a.controller.Snapshot()
	if a.shutdown.Phase() == frontend.ShutdownWaitingForStop {
		if a.shutdown.ObserveExternalStop(state) {
			return
		}
		if !a.shutdown.Active() {
			a.warnMain("Shutdown Cancelled", "The execution changed or did not reach a verified stopped state. DDGo remains open.")
			return
		}
		if state.ProgramStatus == app.ProgramFailed || state.MachineReadiness == app.MachineReadinessRecoveryRequired {
			a.shutdown.Cancel(attempt)
			a.warnMain("Program Stop Failed", "The active program did not reach a verified stopped state. DDGo remains open for recovery.")
		}
		return
	}
	if a.shutdown.Phase() == frontend.ShutdownClosingDocuments {
		if state.ProgramStatus.IsActive() || state.MachineReadiness != app.MachineReadinessNormal {
			a.shutdown.Cancel(attempt)
			a.warnMain("Shutdown Cancelled", "Controller execution or machine recovery became active before documents could close.")
			return
		}
		id, ok := a.shutdown.NextDocumentToClose(attempt)
		if !ok {
			return
		}
		entry, exists := a.documents.Entry(id)
		if !exists {
			a.shutdown.DocumentClosed(attempt, id)
			return
		}
		if !entry.Window.Close() {
			a.shutdown.DocumentCloseRejected(attempt, id)
			a.warnMain("Shutdown Cancelled", "A document refused to close. DDGo remains open.")
		}
		return
	}
	if a.shutdown.Phase() == frontend.ShutdownReadyToCloseMain {
		if a.documents.Len() != 0 || state.ProgramStatus.IsActive() || state.MachineReadiness != app.MachineReadinessNormal {
			a.shutdown.Cancel(attempt)
			a.warnMain("Shutdown Cancelled", "DDGo state changed before final shutdown could be verified.")
			return
		}
		if a.shutdown.CommitMainClose(attempt) {
			// Commit first; WA_DeleteOnClose means no later callback may safely
			// invoke this window again.
			a.mainCloseIssued = true
			mainWindow := a.mainWindow
			a.mainWindow = nil
			mainWindow.Close()
		}
	}
}

func (a *Application) mainParent() *qt.QWidget {
	if a.mainWindow == nil {
		return nil
	}
	return a.mainWindow.window.QWidget
}

func (a *Application) warnMain(title, text string) {
	qt.QMessageBox_Warning(a.mainParent(), title, text)
}

func (a *Application) warnDocumentStopFailure(entry frontend.DocumentEntry, err error) {
	qt.QMessageBox_Warning(documentParent(entry), "Program Still Active", stopFailureText(err))
}

func stopFailureText(err error) string {
	if err != nil {
		return "The program could not be stopped safely; DDGo remains open for recovery: " + err.Error()
	}
	return "The program did not reach a verified stopped state; DDGo remains open for recovery."
}

func documentParent(entry frontend.DocumentEntry) *qt.QWidget {
	if window, ok := entry.Window.(*GCodeWindow); ok {
		return window.window.QWidget
	}
	return nil
}

// Dialogs are modal only for user choices. Controller Stop completion is
// asynchronous and returns through the application's regular UI timer.
func confirmApplicationClose(parent *qt.QWidget, documentCount int) bool {
	message := "Exit DDGo?"
	if documentCount > 0 {
		message = "Close all open documents and exit DDGo?"
	}
	return qt.QMessageBox_Question(parent, "Close DDGo?", message) == qt.QMessageBox__Yes
}

func confirmStopAndClose(parent *qt.QWidget) bool {
	box := qt.NewQMessageBox3(qt.QMessageBox__Warning, "Program Is Still Executing", "Stop the active program safely before closing?")
	box.SetParent(parent)
	stop := box.AddButton2("Stop Program and Close", qt.QMessageBox__DestructiveRole)
	cancel := box.AddButton2("Cancel", qt.QMessageBox__RejectRole)
	box.SetDefaultButton(cancel)
	box.Exec()
	clicked := box.ClickedButton()
	return clicked != nil && clicked.UnsafePointer() == stop.UnsafePointer()
}
