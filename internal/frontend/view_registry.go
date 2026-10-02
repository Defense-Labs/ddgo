package frontend

import "github.com/ianbruene/ddgo/internal/app"

// EventView is a UI surface that presents application state and reacts to
// application events. Implementations may be backed by any UI toolkit; the
// registry deliberately is not.
//
// ApplyState renders the newest controller state accepted by the UI
// dispatcher. Older event snapshots may be ignored.
//
// ApplyEvent handles event-specific behavior. Every event is delivered even
// when its attached State snapshot is older than the state already presented.
type EventView interface {
	ApplyState(app.State)
	ApplyEvent(app.Event)
}

// ViewDispatcher owns the state that has entered the UI event stream and
// distributes state and event-specific behavior to every registered view.
type ViewDispatcher struct {
	state    app.State
	revision app.StateRevision
	views    viewRegistry
}

// NewViewDispatcher returns a dispatcher initialized with the current state
// and its revision.
func NewViewDispatcher(state app.State, revision app.StateRevision) *ViewDispatcher {
	return &ViewDispatcher{state: state, revision: revision}
}

// Register adds a view and immediately applies the dispatcher's current state.
func (d *ViewDispatcher) Register(view EventView) ViewID {
	id := d.views.add(view)
	view.ApplyState(d.state)
	return id
}

// Unregister removes a view from future state and event broadcasts.
func (d *ViewDispatcher) Unregister(id ViewID) {
	d.views.remove(id)
}

// Dispatch applies a newer state before delivering the event to every view.
// Events are delivered even when their attached state is stale.
func (d *ViewDispatcher) Dispatch(event app.Event) {
	if event.StateRevision > d.revision {
		d.state = event.State
		d.revision = event.StateRevision
		d.views.applyState(event.State)
	}
	d.views.applyEvent(event)
}

// ViewID identifies a registered view for later unregistration.
type ViewID uint64

type viewRegistry struct {
	nextID ViewID
	views  map[ViewID]EventView
}

func (r *viewRegistry) add(view EventView) ViewID {
	if r.views == nil {
		r.views = make(map[ViewID]EventView)
	}
	r.nextID++
	r.views[r.nextID] = view
	return r.nextID
}

func (r *viewRegistry) remove(id ViewID) {
	delete(r.views, id)
}

func (r *viewRegistry) applyState(state app.State) {
	for _, view := range r.views {
		view.ApplyState(state)
	}
}

func (r *viewRegistry) applyEvent(event app.Event) {
	for _, view := range r.views {
		view.ApplyEvent(event)
	}
}
