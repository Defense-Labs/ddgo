package ui

import "github.com/ianbruene/ddgo/internal/frontend"

// applicationWindow is a top-level UI window whose lifetime is owned by the
// application. It remains toolkit-independent so ownership can be tested
// without constructing a GUI.
type applicationWindow interface {
	frontend.EventView
	setOnClose(func())
}

type windowID uint64

type managedWindow struct {
	window applicationWindow
	viewID frontend.ViewID
}

// windowManager retains every open top-level window and couples its lifetime
// to its registration with the application's single event dispatcher.
type windowManager struct {
	dispatcher *frontend.ViewDispatcher

	nextID  windowID
	windows map[windowID]managedWindow
}

func (m *windowManager) add(window applicationWindow) windowID {
	if m.windows == nil {
		m.windows = make(map[windowID]managedWindow)
	}

	m.nextID++
	id := m.nextID
	viewID := m.dispatcher.Register(window)
	m.windows[id] = managedWindow{window: window, viewID: viewID}
	window.setOnClose(func() { m.remove(id) })
	return id
}

func (m *windowManager) remove(id windowID) {
	managed, ok := m.windows[id]
	if !ok {
		return
	}
	delete(m.windows, id)
	m.dispatcher.Unregister(managed.viewID)
}

func (m *windowManager) len() int { return len(m.windows) }
