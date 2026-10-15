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

// AddWithClose is Add with an application-owned close hook. The hook runs
// only after the window manager has removed the window and its event view.
// It lets a higher-level owner (such as the document registry) keep its
// logical lifetime synchronized with the native top-level window lifetime.
func (m *WindowManager) AddWithClose(window ApplicationWindow, afterClose func()) {
	m.addWithClose(window, afterClose)
}

func (m *WindowManager) add(window ApplicationWindow) windowID {
	return m.addWithClose(window, nil)
}

func (m *WindowManager) addWithClose(window ApplicationWindow, afterClose func()) windowID {
	if m.windows == nil {
		m.windows = make(map[windowID]managedWindow)
	}

	m.nextID++
	id := m.nextID
	viewID := m.dispatcher.Register(window)
	m.windows[id] = managedWindow{window: window, viewID: viewID}
	window.SetOnClose(func() {
		if m.remove(id) && afterClose != nil {
			afterClose()
		}
	})
	return id
}

func (m *WindowManager) remove(id windowID) bool {
	managed, ok := m.windows[id]
	if !ok {
		return false
	}
	delete(m.windows, id)
	m.dispatcher.Unregister(managed.viewID)
	return true
}

func (m *WindowManager) len() int { return len(m.windows) }
