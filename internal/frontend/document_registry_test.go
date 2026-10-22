package frontend

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/ddpackage"
	"github.com/ianbruene/ddgo/internal/gcode"
)

type fakeDocumentWindow struct {
	fakeApplicationWindow
	focused int
}

func TestDocumentRegistryOpensOnePackageWithStableIdentity(t *testing.T) {
	registry, manager := newTestDocumentRegistry(t)
	path := writeDocument(t, "fixture.dd", "not read by fake loader")
	loads := 0
	var windows []*fakeDocumentWindow
	load := func(path string) (*ddpackage.Package, error) {
		loads++
		return &ddpackage.Package{SourcePath: path, DisplayName: "Fixture"}, nil
	}
	newWindow := func(app.DocumentID, *ddpackage.Package) DocumentWindow {
		window := &fakeDocumentWindow{}
		windows = append(windows, window)
		return window
	}
	first, opened, err := registry.OpenPackage(path, load, newWindow)
	if err != nil || !opened || first.ID == 0 || first.Kind != DocumentKindDDPackage || first.Package == nil {
		t.Fatalf("OpenPackage() = (%+v, %v, %v)", first, opened, err)
	}
	second, opened, err := registry.OpenPackage(path, load, newWindow)
	if err != nil || opened || second.ID != first.ID || loads != 1 || windows[0].focused != 1 {
		t.Fatalf("duplicate package = (%+v, %v, %v), loads=%d focus=%d", second, opened, err, loads, windows[0].focused)
	}
	if registry.Len() != 1 || manager.len() != 1 {
		t.Fatalf("package ownership registry=%d windows=%d", registry.Len(), manager.len())
	}
	staleClose := windows[0].onClose
	windows[0].Close()
	third, opened, err := registry.OpenPackage(path, load, newWindow)
	if err != nil || !opened || third.ID == first.ID || third.Kind != DocumentKindDDPackage {
		t.Fatalf("reopen package = (%+v, %v, %v)", third, opened, err)
	}
	staleClose()
	if entry, ok := registry.Entry(third.ID); !ok || entry.ID != third.ID {
		t.Fatalf("stale package close affected replacement: %+v %v", entry, ok)
	}
}

func TestDocumentRegistryFailedPackageLoadLeavesNoEntry(t *testing.T) {
	registry, manager := newTestDocumentRegistry(t)
	path := writeDocument(t, "broken.dd", "not a package")
	_, opened, err := registry.OpenPackage(path, func(string) (*ddpackage.Package, error) {
		return nil, errors.New("malformed archive")
	}, func(app.DocumentID, *ddpackage.Package) DocumentWindow { return &fakeDocumentWindow{} })
	if err == nil || opened || registry.Len() != 0 || manager.len() != 0 {
		t.Fatalf("failed package load opened=%v err=%v registry=%d windows=%d", opened, err, registry.Len(), manager.len())
	}
}

func (w *fakeDocumentWindow) Focus() { w.focused++ }
func (w *fakeDocumentWindow) Close() bool {
	w.close()
	return true
}

func newTestDocumentRegistry(t *testing.T) (*DocumentRegistry, *WindowManager) {
	t.Helper()
	manager, _ := newTestWindowManager(app.State{})
	return NewDocumentRegistry(manager), manager
}

func writeDocument(t *testing.T, name, source string) string {
	t.Helper()
	return writeDocumentInDir(t, t.TempDir(), name, source)
}

func writeDocumentInDir(t *testing.T, dir, name, source string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}

func testDocumentWindowFactory(windows *[]*fakeDocumentWindow) func(app.DocumentID, gcode.Document) DocumentWindow {
	return func(app.DocumentID, gcode.Document) DocumentWindow {
		window := &fakeDocumentWindow{}
		*windows = append(*windows, window)
		return window
	}
}

func TestDocumentRegistryOpensOneDocumentWithStableIdentity(t *testing.T) {
	registry, manager := newTestDocumentRegistry(t)
	path := writeDocument(t, "first.gcode", "G0 X1\n")
	var windows []*fakeDocumentWindow
	entry, opened, err := registry.OpenFile(path, gcode.LoadDocument, testDocumentWindowFactory(&windows))
	if err != nil || !opened {
		t.Fatalf("OpenFile() = (%+v, %v, %v), want new entry", entry, opened, err)
	}
	if entry.ID == 0 || entry.Document.Source != "G0 X1\n" || registry.Len() != 1 || manager.len() != 1 {
		t.Fatalf("entry/ownership = %+v, registry=%d windows=%d", entry, registry.Len(), manager.len())
	}
	if entry.CanonicalPath == "" || entry.Document.Path != entry.CanonicalPath {
		t.Fatalf("path identity/document path = %q/%q", entry.CanonicalPath, entry.Document.Path)
	}
}

func TestDocumentRegistryDuplicateFocusesWithoutReloading(t *testing.T) {
	registry, manager := newTestDocumentRegistry(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Keep this fixture on the package working directory's volume. Windows
	// filepath.Rel correctly rejects paths crossing drives, while t.TempDir()
	// may be on a different drive in CI.
	fixtureDir, err := os.MkdirTemp(cwd, ".document-registry-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(fixtureDir) })
	path := writeDocumentInDir(t, fixtureDir, "duplicate.gcode", "G0 X1\n")
	loads := 0
	load := func(path string) (gcode.Document, error) {
		loads++
		return gcode.LoadDocument(path)
	}
	var windows []*fakeDocumentWindow
	first, opened, err := registry.OpenFile(path, load, testDocumentWindowFactory(&windows))
	if err != nil || !opened {
		t.Fatalf("first OpenFile() = (%v, %v)", opened, err)
	}
	if err := os.WriteFile(path, []byte("G0 X99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	relativePath, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatal(err)
	}
	second, opened, err := registry.OpenFile(relativePath, load, testDocumentWindowFactory(&windows))
	if err != nil || opened {
		t.Fatalf("duplicate OpenFile() = (%v, %v), want existing", opened, err)
	}
	if first.ID != second.ID || loads != 1 || len(windows) != 1 || windows[0].focused != 1 || manager.len() != 1 {
		t.Fatalf("duplicate result id=%d/%d loads=%d windows=%d focus=%d managed=%d", first.ID, second.ID, loads, len(windows), windows[0].focused, manager.len())
	}
	if second.Document.Source != "G0 X1\n" {
		t.Fatalf("duplicate source = %q, want original source", second.Document.Source)
	}
}

func TestDocumentRegistryCanonicalizesSymlinkAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require privileges on Windows")
	}
	registry, _ := newTestDocumentRegistry(t)
	path := writeDocument(t, "target.gcode", "M5\n")
	alias := filepath.Join(filepath.Dir(path), "alias.gcode")
	if err := os.Symlink(path, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	var windows []*fakeDocumentWindow
	first, opened, err := registry.OpenFile(path, gcode.LoadDocument, testDocumentWindowFactory(&windows))
	if err != nil || !opened {
		t.Fatalf("first OpenFile: %v", err)
	}
	second, opened, err := registry.OpenFile(alias, gcode.LoadDocument, testDocumentWindowFactory(&windows))
	if err != nil || opened || second.ID != first.ID || len(windows) != 1 {
		t.Fatalf("alias result opened=%v err=%v first=%d second=%d windows=%d", opened, err, first.ID, second.ID, len(windows))
	}
}

func TestDocumentRegistryCloseAndReopenUsesNewIdentityAndIgnoresStaleClose(t *testing.T) {
	registry, manager := newTestDocumentRegistry(t)
	path := writeDocument(t, "reopen.gcode", "M3\n")
	var windows []*fakeDocumentWindow
	first, _, err := registry.OpenFile(path, gcode.LoadDocument, testDocumentWindowFactory(&windows))
	if err != nil {
		t.Fatal(err)
	}
	staleClose := windows[0].onClose
	windows[0].Close()
	if registry.Len() != 0 || manager.len() != 0 {
		t.Fatalf("close did not remove entry: registry=%d manager=%d", registry.Len(), manager.len())
	}
	second, opened, err := registry.OpenFile(path, gcode.LoadDocument, testDocumentWindowFactory(&windows))
	if err != nil || !opened || second.ID == first.ID {
		t.Fatalf("reopen = id %d opened=%v err=%v; first=%d", second.ID, opened, err, first.ID)
	}
	staleClose()
	if entry, ok := registry.Entry(second.ID); !ok || entry.ID != second.ID || registry.Len() != 1 {
		t.Fatalf("stale close affected reopened document: %+v open=%v len=%d", entry, ok, registry.Len())
	}
}

func TestDocumentRegistryLoadFailureDoesNotChangeOpenDocuments(t *testing.T) {
	registry, manager := newTestDocumentRegistry(t)
	path := writeDocument(t, "good.gcode", "M5\n")
	var windows []*fakeDocumentWindow
	entry, _, err := registry.OpenFile(path, gcode.LoadDocument, testDocumentWindowFactory(&windows))
	if err != nil {
		t.Fatal(err)
	}
	_, opened, err := registry.OpenFile(filepath.Join(t.TempDir(), "missing.gcode"), gcode.LoadDocument, testDocumentWindowFactory(&windows))
	if err == nil || opened {
		t.Fatalf("missing OpenFile() = opened=%v err=%v", opened, err)
	}
	if got, ok := registry.Entry(entry.ID); !ok || got.Document.Source != "M5\n" || registry.Len() != 1 || manager.len() != 1 {
		t.Fatalf("existing document changed after failed open: %+v open=%v", got, ok)
	}
}

func TestDocumentRegistryClosePendingIsPerDocument(t *testing.T) {
	registry, _ := newTestDocumentRegistry(t)
	firstPath := writeDocument(t, "first.gcode", "M5\n")
	secondPath := writeDocument(t, "second.gcode", "M3\n")
	var windows []*fakeDocumentWindow
	first, _, err := registry.OpenFile(firstPath, gcode.LoadDocument, testDocumentWindowFactory(&windows))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := registry.OpenFile(secondPath, gcode.LoadDocument, testDocumentWindowFactory(&windows))
	if err != nil {
		t.Fatal(err)
	}
	if !registry.MarkClosePending(first.ID) || registry.MarkClosePending(first.ID) || registry.ClosePending(second.ID) {
		t.Fatal("close-pending state was not isolated/idempotent")
	}
	registry.ClearClosePending(first.ID)
	if registry.ClosePending(first.ID) {
		t.Fatal("ClearClosePending did not clear first document")
	}
}
