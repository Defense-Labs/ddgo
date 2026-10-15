//go:build miqt

package miqt

import (
	"github.com/ianbruene/ddgo/internal/app"
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
	w.executionPanel = newExecutionPanel()

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

func newExecutionPanel() *qt.QGroupBox {
	panel := qt.NewQGroupBox(nil)
	panel.SetTitle("Execution")

	layout := qt.NewQVBoxLayout(nil)
	panel.SetLayout(layout.QLayout)

	explanation := qt.NewQLabel(nil)
	explanation.SetText("Document-specific execution controls are not implemented yet.\n\nUse the main DDGo window for existing program controls.")
	explanation.SetWordWrap(true)
	explanation.SetAlignment(qt.AlignLeft | qt.AlignTop)
	layout.AddWidget(explanation.QWidget)
	layout.AddStretch()
	return panel
}

func (w *GCodeWindow) show()                            { w.window.Show() }
func (w *GCodeWindow) SetOnClose(fn func())             { w.onClose = fn }
func (w *GCodeWindow) SetOnCloseRequest(fn func() bool) { w.onCloseRequest = fn }
func (w *GCodeWindow) Focus() {
	w.window.ShowNormal()
	w.window.Raise()
	w.window.ActivateWindow()
}
func (w *GCodeWindow) Close()               { w.window.Close() }
func (w *GCodeWindow) ApplyState(app.State) {}
func (w *GCodeWindow) ApplyEvent(app.Event) {}
