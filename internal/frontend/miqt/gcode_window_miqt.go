//go:build miqt

package miqt

import (
	"fmt"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/frontend"
	"github.com/ianbruene/ddgo/internal/gcode"
	qt "github.com/mappu/miqt/qt6"
)

// GCodeWindow is a read-only top-level view of exactly one loaded document.
type GCodeWindow struct {
	document            gcode.Document
	window              *qt.QMainWindow
	splitter            *qt.QSplitter
	sourceView          *GCodeSourceView
	toolpathPlaceholder *qt.QWidget
	executionPanel      *qt.QGroupBox
	runButton           *qt.QPushButton
	pauseButton         *qt.QPushButton
	resumeButton        *qt.QPushButton
	stopButton          *qt.QPushButton
	statusLabel         *qt.QLabel
	progressLabel       *qt.QLabel
	lineLabel           *qt.QLabel
	errorLabel          *qt.QLabel
	progressBar         *qt.QProgressBar
	followCheck         *qt.QCheckBox

	session         *frontend.ExecutionSession
	sessionSnapshot frontend.ExecutionSessionSnapshot
	detachSession   func()
	onRun           func()
	onPause         func()
	onResume        func()
	onStop          func()

	onClose        func()
	onOpen         func()
	onCloseRequest func() bool
	closeNotified  bool
}

func newGCodeWindow(document gcode.Document, onOpen func()) *GCodeWindow {
	w := &GCodeWindow{
		document: document,
		window:   qt.NewQMainWindow(nil),
		onOpen:   onOpen,
	}
	w.window.SetWindowTitle(document.Name + " — DDGo")
	w.window.Resize(1280, 720)
	w.window.SetAttribute(qt.WA_DeleteOnClose)
	w.sourceView = newGCodeSourceView(document.Source)
	w.toolpathPlaceholder = newToolpathPlaceholder()
	w.executionPanel = w.newExecutionPanel()

	w.splitter = qt.NewQSplitter3(qt.Horizontal)
	w.splitter.AddWidget(w.sourceView.editor.QWidget)
	w.splitter.AddWidget(w.toolpathPlaceholder)
	w.splitter.AddWidget(w.executionPanel.QWidget)
	w.splitter.SetStretchFactor(0, 3)
	w.splitter.SetStretchFactor(1, 5)
	w.splitter.SetStretchFactor(2, 2)
	w.splitter.SetSizes([]int{384, 640, 256})
	w.window.SetCentralWidget(w.splitter.QWidget)

	fileMenu := w.window.MenuBar().AddMenuWithTitle("File")
	openAction := fileMenu.AddActionWithText("Open…")
	openAction.OnTriggered(func() {
		if w.onOpen != nil {
			w.onOpen()
		}
	})
	w.window.OnCloseEvent(func(super func(*qt.QCloseEvent), event *qt.QCloseEvent) {
		if w.onCloseRequest != nil && !w.onCloseRequest() {
			event.SetAccepted(false)
			return
		}
		super(event)
		if event.IsAccepted() && !w.closeNotified {
			// WA_DeleteOnClose can delete the QObject during this close event. The
			// session callback must be released before the next UI event can reach
			// a destroyed native window. A rejected native close remains attached.
			if w.detachSession != nil {
				w.detachSession()
				w.detachSession = nil
			}
			w.closeNotified = true
			if w.onClose != nil {
				w.onClose()
			}
		}
	})
	return w
}

func newToolpathPlaceholder() *qt.QWidget {
	panel := qt.NewQWidget(nil)
	panel.SetStyleSheet("background-color: #666666;")

	layout := qt.NewQVBoxLayout(nil)
	layout.SetContentsMargins(12, 12, 12, 12)
	panel.SetLayout(layout.QLayout)

	message := qt.NewQLabel(nil)
	message.SetText("3D Toolpath View — Not Yet Implemented")
	message.SetAlignment(qt.AlignCenter)
	message.SetStyleSheet("color: #f0f0f0; background-color: transparent;")
	layout.AddWidget(message.QWidget)
	return panel
}

func (w *GCodeWindow) newExecutionPanel() *qt.QGroupBox {
	panel := qt.NewQGroupBox(nil)
	panel.SetTitle("Execution")

	layout := qt.NewQVBoxLayout(nil)
	panel.SetLayout(layout.QLayout)

	w.statusLabel = qt.NewQLabel(nil)
	w.statusLabel.SetWordWrap(true)
	layout.AddWidget(w.statusLabel.QWidget)
	w.progressLabel = qt.NewQLabel(nil)
	layout.AddWidget(w.progressLabel.QWidget)
	w.progressBar = qt.NewQProgressBar(nil)
	w.progressBar.SetMinimum(0)
	w.progressBar.SetMaximum(1)
	layout.AddWidget(w.progressBar.QWidget)
	w.lineLabel = qt.NewQLabel(nil)
	w.lineLabel.SetWordWrap(true)
	layout.AddWidget(w.lineLabel.QWidget)
	w.followCheck = qt.NewQCheckBox(nil)
	w.followCheck.SetText("Follow Execution")
	w.followCheck.SetChecked(true)
	layout.AddWidget(w.followCheck.QWidget)
	for _, item := range []struct {
		text string
		set  func(*qt.QPushButton)
	}{
		{"Run", func(b *qt.QPushButton) { w.runButton = b }},
		{"Pause", func(b *qt.QPushButton) { w.pauseButton = b }},
		{"Resume", func(b *qt.QPushButton) { w.resumeButton = b }},
		{"Stop", func(b *qt.QPushButton) { w.stopButton = b }},
	} {
		button := qt.NewQPushButton(nil)
		button.SetText(item.text)
		item.set(button)
		layout.AddWidget(button.QWidget)
	}
	w.runButton.OnClicked(func() {
		if w.onRun != nil {
			w.onRun()
		}
	})
	w.pauseButton.OnClicked(func() {
		if w.onPause != nil {
			w.onPause()
		}
	})
	w.resumeButton.OnClicked(func() {
		if w.onResume != nil {
			w.onResume()
		}
	})
	w.stopButton.OnClicked(func() {
		if w.onStop != nil {
			w.onStop()
		}
	})
	w.errorLabel = qt.NewQLabel(nil)
	w.errorLabel.SetWordWrap(true)
	w.errorLabel.SetStyleSheet("color: #b00020;")
	layout.AddWidget(w.errorLabel.QWidget)
	layout.AddStretch()
	return panel
}

// SetExecutionSession attaches this window as a presentation-only consumer.
// The Application owns controller goroutines; the session owns all execution
// state and pending-operation coordination shared by every attached view.
func (w *GCodeWindow) SetExecutionSession(session *frontend.ExecutionSession, run, pause, resume, stop func()) {
	if w.detachSession != nil {
		w.detachSession()
	}
	w.session = session
	w.onRun, w.onPause, w.onResume, w.onStop = run, pause, resume, stop
	if session == nil {
		return
	}
	w.detachSession = session.Attach(func(snapshot frontend.ExecutionSessionSnapshot) {
		w.sessionSnapshot = snapshot
		w.renderExecution()
	})
}

func (w *GCodeWindow) renderExecution() {
	controls := w.sessionSnapshot.Controls
	w.runButton.SetEnabled(controls.Run)
	w.pauseButton.SetEnabled(controls.Pause)
	w.resumeButton.SetEnabled(controls.Resume)
	w.stopButton.SetEnabled(controls.Stop)
	presentation := w.sessionSnapshot.Presentation
	w.statusLabel.SetText("Status: " + presentation.StatusText(w.sessionSnapshot.State))
	w.progressLabel.SetText(fmt.Sprintf("Acknowledged commands: %d / %d", presentation.Complete, presentation.Total))
	maximum := presentation.Total
	if maximum < 1 {
		maximum = 1
	}
	w.progressBar.SetMaximum(maximum)
	w.progressBar.SetValue(presentation.Complete)
	if presentation.NextLine > 0 {
		w.lineLabel.SetText(fmt.Sprintf("Next command: source line %d", presentation.NextLine))
	} else if presentation.Status == app.ProgramDraining {
		w.lineLabel.SetText("All commands acknowledged — awaiting Idle")
	} else {
		w.lineLabel.SetText("Next command: none")
	}
	// This is intentionally called for zero too: completed, draining, stopped,
	// and failed runs must not leave a stale pending-command marker behind.
	w.sourceView.SetExecutionHighlight(presentation.NextLine, w.followCheck.IsChecked())
	w.errorLabel.SetText(presentation.Error)
}

func (w *GCodeWindow) show()                            { w.window.Show() }
func (w *GCodeWindow) SetOnClose(fn func())             { w.onClose = fn }
func (w *GCodeWindow) SetOnCloseRequest(fn func() bool) { w.onCloseRequest = fn }
func (w *GCodeWindow) Focus() {
	w.window.ShowNormal()
	w.window.Raise()
	w.window.ActivateWindow()
}
func (w *GCodeWindow) Close() bool { return w.window.Close() }

// Controller events are routed once through ExecutionSessions. These no-op
// EventView methods retain WindowManager registration without giving each
// native window a second competing execution history.
func (w *GCodeWindow) ApplyState(frontendState app.State) {}
func (w *GCodeWindow) ApplyEvent(frontendEvent app.Event) {}
