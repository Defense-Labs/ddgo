package frontend

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/ddpackage"
	"github.com/ianbruene/ddgo/internal/gcode"
)

// DocumentWindow is the small toolkit-neutral surface the document registry
// needs. WindowManager remains responsible for retaining the top-level window
// and registering it with the single frontend event dispatcher.
type DocumentWindow interface {
	ApplicationWindow
	Focus()
	Close() bool
}

// DocumentKind identifies the retained, first-class document payload. Native
// auxiliary views intentionally do not have a DocumentKind or DocumentID.
type DocumentKind uint8

const (
	DocumentKindGCode DocumentKind = iota + 1
	DocumentKindDDPackage
)

// DocumentEntry is one currently open file-backed document. Its ID is an
// application identity, not a filename or a controller RunID.
type DocumentEntry struct {
	ID            app.DocumentID
	Kind          DocumentKind
	Document      gcode.Document
	Package       *ddpackage.Package
	CanonicalPath string
	Window        DocumentWindow
	closePending  bool
}

// DocumentRegistry owns logical document identities and the one-open-window
// lookup for canonical file identities. It is used from the UI thread; window
// lifetime itself remains owned by WindowManager.
type DocumentRegistry struct {
	windows *WindowManager
	nextID  app.DocumentID
	byID    map[app.DocumentID]*DocumentEntry
	byPath  map[string]app.DocumentID
	onClose func(DocumentEntry)
}

func NewDocumentRegistry(windows *WindowManager) *DocumentRegistry {
	return &DocumentRegistry{windows: windows}
}

// SetOnClose receives the removed entry after its native window has closed
// and WindowManager has unregistered its event view.
func (r *DocumentRegistry) SetOnClose(fn func(DocumentEntry)) { r.onClose = fn }

// CanonicalDocumentPath returns the identity used for a file-backed document.
// Existing paths are resolved through symlinks, so relative, clean, and alias
// spellings converge before a document is read.
func CanonicalDocumentPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("document path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve document path: %w", err)
	}
	abs = filepath.Clean(abs)
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve document symlinks: %w", err)
	}
	resolved = filepath.Clean(resolved)
	if runtime.GOOS == "windows" {
		// Windows paths are ordinarily case-insensitive. Keep this policy local
		// to the file identity rather than changing document display metadata.
		resolved = strings.ToLower(resolved)
	}
	return resolved, nil
}

// OpenFile focuses an already-open canonical path without rereading it.
// Otherwise it loads, assigns a fresh nonzero DocumentID, creates one window,
// and registers that window with WindowManager as one atomic UI-thread step.
// The returned boolean reports whether a new document was opened.
func (r *DocumentRegistry) OpenFile(path string, load func(string) (gcode.Document, error), newWindow func(app.DocumentID, gcode.Document) DocumentWindow) (DocumentEntry, bool, error) {
	return r.open(path, func(canonical string, id app.DocumentID) (DocumentEntry, error) {
		document, err := load(canonical)
		if err != nil {
			return DocumentEntry{}, err
		}
		window := newWindow(id, document)
		if window == nil {
			return DocumentEntry{}, fmt.Errorf("create document window")
		}
		return DocumentEntry{ID: id, Kind: DocumentKindGCode, Document: document, CanonicalPath: canonical, Window: window}, nil
	})
}

// OpenPackage is the package analogue of OpenFile. It preserves the fully
// loaded package snapshot supplied by the loader and creates one logical
// document regardless of the number of later auxiliary G-code views.
func (r *DocumentRegistry) OpenPackage(path string, load func(string) (*ddpackage.Package, error), newWindow func(app.DocumentID, *ddpackage.Package) DocumentWindow) (DocumentEntry, bool, error) {
	return r.open(path, func(canonical string, id app.DocumentID) (DocumentEntry, error) {
		pkg, err := load(canonical)
		if err != nil {
			return DocumentEntry{}, err
		}
		if pkg == nil {
			return DocumentEntry{}, fmt.Errorf("load .dd package returned nil")
		}
		window := newWindow(id, pkg)
		if window == nil {
			return DocumentEntry{}, fmt.Errorf("create package window")
		}
		return DocumentEntry{ID: id, Kind: DocumentKindDDPackage, Package: pkg, CanonicalPath: canonical, Window: window}, nil
	})
}

func (r *DocumentRegistry) open(path string, create func(canonical string, id app.DocumentID) (DocumentEntry, error)) (DocumentEntry, bool, error) {
	if r == nil || r.windows == nil {
		return DocumentEntry{}, false, fmt.Errorf("document registry is not initialized")
	}
	canonical, err := CanonicalDocumentPath(path)
	if err != nil {
		return DocumentEntry{}, false, err
	}
	if id, ok := r.byPath[canonical]; ok {
		if entry, ok := r.byID[id]; ok {
			entry.Window.Focus()
			return *entry, false, nil
		}
		delete(r.byPath, canonical)
	}
	if r.nextID == ^app.DocumentID(0) {
		return DocumentEntry{}, false, fmt.Errorf("document IDs exhausted")
	}
	candidate := r.nextID + 1
	if candidate == 0 {
		candidate++
	}
	entry, err := create(canonical, candidate)
	if err != nil {
		return DocumentEntry{}, false, err
	}
	if entry.ID != candidate || entry.Window == nil {
		return DocumentEntry{}, false, fmt.Errorf("invalid document registration")
	}
	r.nextID = candidate
	if r.byID == nil {
		r.byID = make(map[app.DocumentID]*DocumentEntry)
		r.byPath = make(map[string]app.DocumentID)
	}
	r.byID[entry.ID] = &entry
	r.byPath[canonical] = entry.ID
	r.windows.AddWithClose(entry.Window, func() { r.remove(entry.ID) })
	return entry, true, nil
}

// Entry returns the current entry by stable application identity.
func (r *DocumentRegistry) Entry(id app.DocumentID) (DocumentEntry, bool) {
	entry, ok := r.byID[id]
	if !ok {
		return DocumentEntry{}, false
	}
	return *entry, true
}

// Entries returns a value copy of the current logical documents.
func (r *DocumentRegistry) Entries() []DocumentEntry {
	entries := make([]DocumentEntry, 0, len(r.byID))
	for _, entry := range r.byID {
		entries = append(entries, *entry)
	}
	return entries
}

func (r *DocumentRegistry) Len() int { return len(r.byID) }

// MarkClosePending makes repeated native close requests idempotent while the
// application waits for a run-aware Stop result.
func (r *DocumentRegistry) MarkClosePending(id app.DocumentID) bool {
	entry, ok := r.byID[id]
	if !ok || entry.closePending {
		return false
	}
	entry.closePending = true
	return true
}

func (r *DocumentRegistry) ClearClosePending(id app.DocumentID) {
	if entry, ok := r.byID[id]; ok {
		entry.closePending = false
	}
}

func (r *DocumentRegistry) ClosePending(id app.DocumentID) bool {
	entry, ok := r.byID[id]
	return ok && entry.closePending
}

func (r *DocumentRegistry) remove(id app.DocumentID) {
	entry, ok := r.byID[id]
	if !ok {
		return
	}
	delete(r.byID, id)
	if current, ok := r.byPath[entry.CanonicalPath]; ok && current == id {
		delete(r.byPath, entry.CanonicalPath)
	}
	if r.onClose != nil {
		r.onClose(*entry)
	}
}
