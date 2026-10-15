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
	mainWindow *MainWindow

	pendingDocumentCloses  map[app.DocumentID]app.RunID
	approvedDocumentCloses map[app.DocumentID]bool
	documentCloseResults   chan documentCloseResult

	shutdown            frontend.ShutdownCoordinator
	shutdownStopResults chan shutdownStopResult
	mainCloseIssued     bool
}

type documentCloseResult struct {
	documentID app.DocumentID
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
	a.pendingDocumentCloses = make(map[app.DocumentID]app.RunID)
	a.approvedDocumentCloses = make(map[app.DocumentID]bool)
	a.documentCloseResults = make(chan documentCloseResult, 16)
	a.shutdownStopResults = make(chan shutdownStopResult, 2)

	mainWindow := newMainWindow(a.controller, func() { a.openGCodeFile() })
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
		entry.Window.Focus()
	}
}

func (a *Application) drainControllerEvents() {
	for {
		select {
		case event := <-a.controller.Events():
			a.dispatcher.Dispatch(event)
		default:
			a.drainDocumentCloseResults()
			a.drainShutdownStopResults()
			a.progressShutdown()
			return
		}
	}
}

// requestDocumentClose governs independent document closure. An application
// shutdown is the sole closer while its coordinator is active, preventing a
// second document Stop request or an accidental partial shutdown.
func (a *Application) requestDocumentClose(id app.DocumentID) bool {
	if a.approvedDocumentCloses[id] {
		return true
	}
	if a.shutdown.Active() {
		return a.shutdown.AllowsDocumentClose(a.shutdown.Attempt(), id)
	}
	entry, ok := a.documents.Entry(id)
	if !ok {
		return true
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
			if result.err != nil || !stillOpen || state.ProgramDocumentID != result.documentID || state.ProgramRunID != result.runID ||
				state.ProgramStatus != app.ProgramStopped || state.MachineReadiness != app.MachineReadinessNormal {
				a.documents.ClearClosePending(result.documentID)
				if stillOpen {
					a.warnDocumentStopFailure(entry, result.err)
				}
				continue
			}
			a.approvedDocumentCloses[result.documentID] = true
			if !entry.Window.Close() {
				delete(a.approvedDocumentCloses, result.documentID)
				a.warnDocumentStopFailure(entry, nil)
			}
		default:
			return
		}
	}
}

func (a *Application) documentClosed(entry frontend.DocumentEntry) {
	delete(a.pendingDocumentCloses, entry.ID)
	delete(a.approvedDocumentCloses, entry.ID)
	// Do not close the main window here: this callback runs inside native
	// document destruction. The next UI timer turn advances the coordinator.
	if a.shutdown.Active() {
		a.shutdown.DocumentClosed(a.shutdown.Attempt(), entry.ID)
	}
}

func (a *Application) requestMainClose() bool {
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
	target := frontend.ShutdownStopTarget{}
	if state.ProgramStatus.IsActive() {
		target = frontend.ShutdownStopTarget{DocumentID: state.ProgramDocumentID, RunID: state.ProgramRunID}
	}
	attempt, ok := a.shutdown.Begin(documentIDs, target)
	if !ok {
		return false
	}
	if !confirmApplicationClose(a.mainParent(), len(documentIDs)) {
		a.shutdown.ConfirmApplication(attempt, false)
		return false
	}
	phase := a.shutdown.ConfirmApplication(attempt, true)
	if phase == frontend.ShutdownWaitingForStopConfirmation {
		if !confirmStopAndClose(a.mainParent()) {
			a.shutdown.ConfirmStop(attempt, false)
			return false
		}
		if target, ok := a.shutdown.ConfirmStop(attempt, true); ok {
			a.startShutdownStop(attempt, target)
		}
	}
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
	if !a.shutdown.Active() || a.mainCloseIssued {
		return
	}
	attempt := a.shutdown.Attempt()
	state := a.controller.Snapshot()
	if a.shutdown.Phase() == frontend.ShutdownWaitingForStop {
		if a.shutdown.ObserveExternalStop(state) {
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
