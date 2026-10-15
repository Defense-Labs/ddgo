package frontend

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ianbruene/ddgo/internal/app"
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

// DocumentEntry is one currently open file-backed document. Its ID is an
// application identity, not a filename or a controller RunID.
type DocumentEntry struct {
	ID            app.DocumentID
	Document      gcode.Document
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
		// Keep maps self-healing if an interrupted earlier teardown left a stale
		// lookup. A live entry is always present in both maps.
		delete(r.byPath, canonical)
	}
	document, err := load(canonical)
	if err != nil {
		return DocumentEntry{}, false, err
	}
	if r.nextID == ^app.DocumentID(0) {
		return DocumentEntry{}, false, fmt.Errorf("document IDs exhausted")
	}
	r.nextID++
	if r.nextID == 0 {
		r.nextID++
	}
	window := newWindow(r.nextID, document)
	if window == nil {
		return DocumentEntry{}, false, fmt.Errorf("create document window")
	}
	entry := &DocumentEntry{ID: r.nextID, Document: document, CanonicalPath: canonical, Window: window}
	if r.byID == nil {
		r.byID = make(map[app.DocumentID]*DocumentEntry)
		r.byPath = make(map[string]app.DocumentID)
	}
	r.byID[entry.ID] = entry
	r.byPath[canonical] = entry.ID
	r.windows.AddWithClose(window, func() { r.remove(entry.ID) })
	return *entry, true, nil
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
