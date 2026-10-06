package ui

import (
	"reflect"
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/frontend"
)

type fakeApplicationWindow struct {
	states  []app.State
	events  []app.Event
	onClose func()
}

func (w *fakeApplicationWindow) ApplyState(state app.State) { w.states = append(w.states, state) }
func (w *fakeApplicationWindow) ApplyEvent(event app.Event) { w.events = append(w.events, event) }
func (w *fakeApplicationWindow) SetOnClose(fn func())       { w.onClose = fn }
func (w *fakeApplicationWindow) close()                     { w.onClose() }

func newTestWindowManager(state app.State) (*windowManager, *frontend.ViewDispatcher) {
	dispatcher := frontend.NewViewDispatcher(state, 0)
	return &windowManager{dispatcher: dispatcher}, dispatcher
}

func TestWindowManagerRegistersWindowAndSuppliesInitialState(t *testing.T) {
	t.Parallel()
	initial := app.State{PortName: "initial"}
	manager, _ := newTestWindowManager(initial)
	window := &fakeApplicationWindow{}
	id := manager.add(window)

	if id == 0 || manager.len() != 1 {
		t.Fatalf("id = %d, managed = %d; want nonzero, 1", id, manager.len())
	}
	if !reflect.DeepEqual(window.states, []app.State{initial}) {
		t.Fatalf("initial states = %#v, want %#v", window.states, []app.State{initial})
	}
}

func TestWindowManagerSupportsMultipleWindowsAndBroadcasts(t *testing.T) {
	t.Parallel()
	manager, dispatcher := newTestWindowManager(app.State{})
	windows := []*fakeApplicationWindow{{}, {}, {}}
	ids := make(map[windowID]bool)
	for _, window := range windows {
		ids[manager.add(window)] = true
	}
	if len(ids) != len(windows) || manager.len() != len(windows) {
		t.Fatalf("unique IDs = %d, managed = %d; want %d each", len(ids), manager.len(), len(windows))
	}

	event := app.Event{Kind: app.EventStateChanged, Text: "broadcast"}
	dispatcher.Dispatch(event)
	for i, window := range windows {
		if !reflect.DeepEqual(window.events, []app.Event{event}) {
			t.Errorf("window %d events = %#v, want %#v", i, window.events, []app.Event{event})
		}
	}
}

func TestWindowManagerCloseRemovesOnlyClosedWindowAndIsIdempotent(t *testing.T) {
	t.Parallel()
	manager, dispatcher := newTestWindowManager(app.State{})
	windows := []*fakeApplicationWindow{{}, {}, {}}
	ids := make([]windowID, len(windows))
	for i, window := range windows {
		ids[i] = manager.add(window)
	}

	windows[1].close()
	windows[1].close()
	manager.remove(ids[1])
	if manager.len() != 2 {
		t.Fatalf("managed = %d; want 2", manager.len())
	}
	if _, ok := manager.windows[ids[0]]; !ok {
		t.Error("first unrelated window was removed")
	}
	if _, ok := manager.windows[ids[2]]; !ok {
		t.Error("third unrelated window was removed")
	}

	event := app.Event{Kind: app.EventError, Text: "after close"}
	dispatcher.Dispatch(event)
	if len(windows[1].events) != 0 {
		t.Fatalf("closed window received %d events, want 0", len(windows[1].events))
	}
	if len(windows[0].events) != 1 || len(windows[2].events) != 1 {
		t.Fatalf("survivor event counts = %d, %d; want 1, 1", len(windows[0].events), len(windows[2].events))
	}
}

func TestWindowManagerRetainsLargeArbitraryCountAndRemovesAll(t *testing.T) {
	t.Parallel()
	manager, _ := newTestWindowManager(app.State{})
	const count = 100
	windows := make([]*fakeApplicationWindow, count)
	ids := make(map[windowID]struct{}, count)
	for i := range windows {
		windows[i] = &fakeApplicationWindow{}
		ids[manager.add(windows[i])] = struct{}{}
	}
	if len(ids) != count || manager.len() != count {
		t.Fatalf("unique IDs = %d, managed = %d; want %d each", len(ids), manager.len(), count)
	}
	for _, window := range windows {
		window.close()
	}
	if manager.len() != 0 {
		t.Fatalf("managed = %d after closing all; want 0", manager.len())
	}
}
