//go:build miqt

package ui

import (
	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/gcode"
	qt "github.com/mappu/miqt/qt"
)

// GCodeWindow is a read-only top-level view of exactly one loaded document.
type GCodeWindow struct {
	document   gcode.Document
	window     *qt.QMainWindow
	sourceView *GCodeSourceView

	onClose       func()
	onOpen        func()
	closeNotified bool
}

func newGCodeWindow(document gcode.Document, onOpen func()) *GCodeWindow {
	w := &GCodeWindow{
		document: document,
		window:   qt.NewQMainWindow(nil),
		onOpen:   onOpen,
	}
	w.window.SetWindowTitle(document.Name + " — DDGo")
	w.window.Resize(800, 600)
	w.window.SetAttribute(qt.WA_DeleteOnClose)
	w.sourceView = newGCodeSourceView(document.Source)
	w.window.SetCentralWidget(w.sourceView.editor.QWidget)

	fileMenu := w.window.MenuBar().AddMenuWithTitle("File")
	openAction := fileMenu.AddAction("Open…")
	openAction.OnTriggered(func() {
		if w.onOpen != nil {
			w.onOpen()
		}
	})
	w.window.OnCloseEvent(func(super func(*qt.QCloseEvent), event *qt.QCloseEvent) {
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

func (w *GCodeWindow) show()                { w.window.Show() }
func (w *GCodeWindow) setOnClose(fn func()) { w.onClose = fn }
func (w *GCodeWindow) applyState(app.State) {}
func (w *GCodeWindow) applyEvent(app.Event) {}
