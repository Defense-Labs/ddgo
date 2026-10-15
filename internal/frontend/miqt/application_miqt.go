//go:build miqt

package miqt

import (
	"context"
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

	pendingDocumentCloses  map[app.DocumentID]app.RunID
	approvedDocumentCloses map[app.DocumentID]bool
	documentCloseResults   chan documentCloseResult
	pendingMainStop        bool
	mainStopResults        chan error
	shutdownRequested      bool
	mainCloseApproved      bool
	mainWindow             *MainWindow
}

type documentCloseResult struct {
	documentID app.DocumentID
	runID      app.RunID
	err        error
}

func newApplication(controller *app.Controller) *Application {
	return &Application{controller: controller}
}

// Run creates and runs the UI application.
func Run(controller *app.Controller) error {
	return newApplication(controller).Run()
}

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
	a.mainStopResults = make(chan error, 1)

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
			a.drainMainStopResults()
			return
		}
	}
}

func (a *Application) requestDocumentClose(id app.DocumentID) bool {
	if a.approvedDocumentCloses[id] {
		return true
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
	if !confirmStopAndClose(documentParent(entry)) {
		return false
	}
	if !a.documents.MarkClosePending(id) {
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
					reason := "the program could not be stopped safely; the document remains open for recovery"
					if result.err != nil {
						reason = reason + ": " + result.err.Error()
					}
					qt.QMessageBox_Warning(documentParent(entry), "Program Still Active", reason)
				}
				continue
			}
			a.approvedDocumentCloses[result.documentID] = true
			entry.Window.Close()
		default:
			return
		}
	}
}

func (a *Application) documentClosed(entry frontend.DocumentEntry) {
	delete(a.pendingDocumentCloses, entry.ID)
	delete(a.approvedDocumentCloses, entry.ID)
	if a.shutdownRequested {
		a.maybeFinishShutdown()
	}
}

func (a *Application) requestMainClose() bool {
	if a.mainCloseApproved {
		return true
	}
	state := a.controller.Snapshot()
	if state.MachineReadiness != app.MachineReadinessNormal {
		qt.QMessageBox_Warning(a.mainWindow.window.QWidget, "Machine Recovery Required", "DDGo has not verified the controller's safe condition. Complete recovery before exiting.")
		return false
	}
	if a.documents.Len() > 0 {
		if !a.shutdownRequested {
			answer := qt.QMessageBox_Question(a.mainWindow.window.QWidget, "Close DDGo?", "Close all open documents and exit DDGo?")
			if answer != qt.QMessageBox__Yes {
				return false
			}
			a.shutdownRequested = true
		}
		for _, entry := range a.documents.Entries() {
			entry.Window.Close()
		}
		a.maybeFinishShutdown()
		return false
	}
	if state.ProgramStatus.IsActive() {
		if !confirmStopAndClose(a.mainWindow.window.QWidget) {
			return false
		}
		a.pendingMainStop = true
		go func() { a.mainStopResults <- a.controller.StopProgram(context.Background()) }()
		return false
	}
	a.mainCloseApproved = true
	return true
}

func (a *Application) drainMainStopResults() {
	if !a.pendingMainStop {
		return
	}
	select {
	case err := <-a.mainStopResults:
		a.pendingMainStop = false
		if err != nil || a.controller.Snapshot().MachineReadiness != app.MachineReadinessNormal {
			reason := "the legacy program could not be stopped safely; DDGo remains open for recovery"
			if err != nil {
				reason += ": " + err.Error()
			}
			qt.QMessageBox_Warning(a.mainWindow.window.QWidget, "Program Still Active", reason)
			return
		}
		a.mainCloseApproved = true
		a.mainWindow.Close()
	default:
	}
}

func (a *Application) maybeFinishShutdown() {
	if !a.shutdownRequested || a.documents.Len() != 0 || a.pendingMainStop {
		return
	}
	state := a.controller.Snapshot()
	if state.ProgramStatus.IsActive() || state.MachineReadiness != app.MachineReadinessNormal {
		return
	}
	a.mainCloseApproved = true
	a.mainWindow.Close()
}

// confirmStopAndClose is modal only for the user decision. It never waits for
// controller I/O; Stop completion returns through Application's UI timer.
func documentParent(entry frontend.DocumentEntry) *qt.QWidget {
	if window, ok := entry.Window.(*GCodeWindow); ok {
		return window.window.QWidget
	}
	return nil
}

func confirmStopAndClose(parent *qt.QWidget) bool {
	box := qt.NewQMessageBox3(qt.QMessageBox__Warning, "Program Is Still Executing", "This document owns an active program. Stop the program safely before closing?")
	box.SetParent(parent)
	stop := box.AddButton2("Stop Program and Close", qt.QMessageBox__DestructiveRole)
	cancel := box.AddButton2("Cancel", qt.QMessageBox__RejectRole)
	box.SetDefaultButton(cancel)
	box.Exec()
	clicked := box.ClickedButton()
	return clicked != nil && clicked.UnsafePointer() == stop.UnsafePointer()
}
