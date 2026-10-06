package frontend

// ApplicationWindow is a top-level UI window whose lifetime is owned by the
// application. It remains toolkit-independent so ownership can be tested
// without constructing a GUI.
type ApplicationWindow interface {
	EventView
	SetOnClose(func())
}

type windowID uint64

type managedWindow struct {
	window ApplicationWindow
	viewID ViewID
}

// WindowManager retains every open top-level window and couples its lifetime
// to its registration with the application's single event dispatcher.
type WindowManager struct {
	dispatcher *ViewDispatcher

	nextID  windowID
	windows map[windowID]managedWindow
}

// NewWindowManager returns a window manager associated with dispatcher.
func NewWindowManager(dispatcher *ViewDispatcher) *WindowManager {
	return &WindowManager{dispatcher: dispatcher}
}

// Add retains window, registers it for frontend events, and removes it when
// its close callback runs.
func (m *WindowManager) Add(window ApplicationWindow) {
	m.add(window)
}

func (m *WindowManager) add(window ApplicationWindow) windowID {
	if m.windows == nil {
		m.windows = make(map[windowID]managedWindow)
	}

	m.nextID++
	id := m.nextID
	viewID := m.dispatcher.Register(window)
	m.windows[id] = managedWindow{window: window, viewID: viewID}
	window.SetOnClose(func() { m.remove(id) })
	return id
}

func (m *WindowManager) remove(id windowID) {
	managed, ok := m.windows[id]
	if !ok {
		return
	}
	delete(m.windows, id)
	m.dispatcher.Unregister(managed.viewID)
}

func (m *WindowManager) len() int { return len(m.windows) }
