//go:build miqt

package ui

import (
	"context"
	"fmt"
	"os"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/gcode"
	qt "github.com/mappu/miqt/qt"
)

// Application owns the Qt lifetime, top-level windows, and the single UI-side
// subscription to controller events.
type Application struct {
	controller *app.Controller

	pollTimer  *qt.QTimer
	dispatcher viewDispatcher
	windows    windowManager
}

func newApplication(controller *app.Controller) *Application {
	a := &Application{controller: controller}
	a.windows.dispatcher = &a.dispatcher
	return a
}

// Run creates and runs the UI application.
func Run(controller *app.Controller) error {
	return newApplication(controller).Run()
}

func (a *Application) Run() error {
	qt.NewQApplication(os.Args)

	state, revision := a.controller.SnapshotWithRevision()
	a.dispatcher.state = state
	a.dispatcher.revision = revision
	mainWindow := newMainWindow(a.controller, func() { a.openGCodeFile() })
	a.windows.add(mainWindow)
	mainWindow.show()

	a.pollTimer = qt.NewQTimer()
	a.pollTimer.OnTimeout(func() { a.drainControllerEvents() })
	a.pollTimer.Start(50)

	monitorContext, stopMonitor := context.WithCancel(context.Background())
	a.controller.StartPortMonitoring(monitorContext)
	qt.QApplication_Exec()
	stopMonitor()
	a.controller.StopPortMonitoring()
	return nil
}

func (a *Application) openGCodeFile() {
	path := chooseProgramFile(nil)
	if path == "" {
		return
	}
	document, err := gcode.LoadDocument(path)
	if err != nil {
		qt.QMessageBox_Critical(nil, "Unable to Open File", fmt.Sprintf("Could not open %s:\n%v", path, err))
		return
	}
	a.openGCodeDocument(document)
}

func (a *Application) openGCodeDocument(document gcode.Document) {
	window := newGCodeWindow(document, func() { a.openGCodeFile() })
	a.windows.add(window)
	window.show()
}

func (a *Application) drainControllerEvents() {
	for {
		select {
		case event := <-a.controller.Events():
			a.dispatcher.dispatch(event)
		default:
			return
		}
	}
}
