package frontend

import (
	"fmt"

	"github.com/ianbruene/ddgo/internal/app"
)

// PackageAuxiliaryViews records native supplementary views owned by package
// documents. They are deliberately not DocumentRegistry entries: a package
// remains one logical shutdown/document-close participant regardless of how
// many step views are open. Tokens make a delayed native close callback unable
// to remove a replacement view for the same package step.
type PackageAuxiliaryViews struct {
	next    uint64
	byOwner map[app.DocumentID]map[ExecutionSessionID]packageAuxiliaryView
}

type packageAuxiliaryView struct {
	window DocumentWindow
	token  uint64
}

func (v *PackageAuxiliaryViews) Add(owner app.DocumentID, id ExecutionSessionID, window DocumentWindow) (token uint64, existing DocumentWindow, added bool, err error) {
	if owner == 0 || owner != id.DocumentID || id.Standalone() || !id.Valid() || window == nil {
		return 0, nil, false, fmt.Errorf("invalid package auxiliary view identity")
	}
	if current := v.byOwner[owner]; current != nil {
		if entry, ok := current[id]; ok {
			return entry.token, entry.window, false, nil
		}
	}
	v.next++
	if v.next == 0 {
		v.next++
	}
	if v.byOwner == nil {
		v.byOwner = make(map[app.DocumentID]map[ExecutionSessionID]packageAuxiliaryView)
	}
	if v.byOwner[owner] == nil {
		v.byOwner[owner] = make(map[ExecutionSessionID]packageAuxiliaryView)
	}
	v.byOwner[owner][id] = packageAuxiliaryView{window: window, token: v.next}
	return v.next, nil, true, nil
}

func (v *PackageAuxiliaryViews) Window(owner app.DocumentID, id ExecutionSessionID) (DocumentWindow, bool) {
	entry, ok := v.byOwner[owner][id]
	return entry.window, ok
}

// Remove only accepts the registration token returned by Add. This is needed
// when a native view's close callback is delayed beyond a new view opening.
func (v *PackageAuxiliaryViews) Remove(owner app.DocumentID, id ExecutionSessionID, token uint64) bool {
	views := v.byOwner[owner]
	entry, ok := views[id]
	if !ok || entry.token != token {
		return false
	}
	delete(views, id)
	if len(views) == 0 {
		delete(v.byOwner, owner)
	}
	return true
}

// CloseOwner asks every currently registered auxiliary view to close without
// discarding membership first. Accepted native closes invoke their individual
// WindowManager callback, which calls Remove with the original token. A false
// result or a missing callback leaves the surviving view owned and prevents
// the package's native close from being accepted.
func (v *PackageAuxiliaryViews) CloseOwner(owner app.DocumentID) bool {
	type pendingClose struct {
		id     ExecutionSessionID
		token  uint64
		window DocumentWindow
	}
	views := v.byOwner[owner]
	pending := make([]pendingClose, 0, len(views))
	for id, entry := range views {
		pending = append(pending, pendingClose{id: id, token: entry.token, window: entry.window})
	}
	for _, close := range pending {
		current, ok := v.byOwner[owner][close.id]
		if !ok || current.token != close.token {
			continue
		}
		if !close.window.Close() {
			return false
		}
	}
	return len(v.byOwner[owner]) == 0
}

func (v *PackageAuxiliaryViews) Len(owner app.DocumentID) int { return len(v.byOwner[owner]) }
