package frontend

import (
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/ddpackage"
)

func openTestPackage(t *testing.T, registry *DocumentRegistry, windows *[]*fakeDocumentWindow) DocumentEntry {
	t.Helper()
	path := writeDocument(t, "fixture.dd", "fixture")
	entry, opened, err := registry.OpenPackage(path, func(path string) (*ddpackage.Package, error) {
		return &ddpackage.Package{SourcePath: path, DisplayName: "Fixture"}, nil
	}, func(app.DocumentID, *ddpackage.Package) DocumentWindow {
		window := &fakeDocumentWindow{}
		*windows = append(*windows, window)
		return window
	})
	if err != nil || !opened {
		t.Fatalf("OpenPackage = opened=%v err=%v", opened, err)
	}
	return entry
}

func addTestAuxiliary(t *testing.T, manager *WindowManager, views *PackageAuxiliaryViews, owner app.DocumentID, id ExecutionSessionID, reject bool) *fakeDocumentWindow {
	t.Helper()
	window := &fakeDocumentWindow{rejectClose: reject}
	token, _, added, err := views.Add(owner, id, window)
	if err != nil || !added {
		t.Fatalf("Add auxiliary added=%v err=%v", added, err)
	}
	manager.AddWithClose(window, func() { views.Remove(owner, id, token) })
	return window
}

func closeTestPackage(entry DocumentEntry, views *PackageAuxiliaryViews) bool {
	return views.CloseOwner(entry.ID) && entry.Window.Close()
}

func TestPackageLifecycleSuccessfulCloseReleasesOwnedViews(t *testing.T) {
	registry, manager := newTestDocumentRegistry(t)
	var packageWindows []*fakeDocumentWindow
	entry := openTestPackage(t, registry, &packageWindows)
	var views PackageAuxiliaryViews
	addTestAuxiliary(t, manager, &views, entry.ID, PackageStepExecutionSessionID(entry.ID, 1, "one"), false)
	addTestAuxiliary(t, manager, &views, entry.ID, PackageStepExecutionSessionID(entry.ID, 2, "two"), false)
	if registry.Len() != 1 || manager.len() != 3 {
		t.Fatalf("before close registry=%d windows=%d", registry.Len(), manager.len())
	}
	if !closeTestPackage(entry, &views) {
		t.Fatal("authorized package close failed")
	}
	if registry.Len() != 0 || manager.len() != 0 || views.Len(entry.ID) != 0 {
		t.Fatalf("after close registry=%d windows=%d memberships=%d", registry.Len(), manager.len(), views.Len(entry.ID))
	}
}

func TestPackageLifecycleRefusingAuxiliaryKeepsPackageOwned(t *testing.T) {
	registry, manager := newTestDocumentRegistry(t)
	var packageWindows []*fakeDocumentWindow
	entry := openTestPackage(t, registry, &packageWindows)
	var views PackageAuxiliaryViews
	id := PackageStepExecutionSessionID(entry.ID, 1, "one")
	refusing := addTestAuxiliary(t, manager, &views, entry.ID, id, true)
	if closeTestPackage(entry, &views) {
		t.Fatal("package close succeeded despite refusing auxiliary")
	}
	if _, open := registry.Entry(entry.ID); !open || views.Len(entry.ID) != 1 || manager.len() != 2 {
		t.Fatalf("refusal lost ownership registry=%v memberships=%d windows=%d", open, views.Len(entry.ID), manager.len())
	}
	if current, ok := views.Window(entry.ID, id); !ok || current != refusing {
		t.Fatal("refusing auxiliary is no longer associated with package")
	}
}

func TestPackageLifecycleRejectedPackageCloseLeavesAuxiliariesUntouched(t *testing.T) {
	registry, manager := newTestDocumentRegistry(t)
	var packageWindows []*fakeDocumentWindow
	entry := openTestPackage(t, registry, &packageWindows)
	var views PackageAuxiliaryViews
	idA := PackageStepExecutionSessionID(entry.ID, 1, "one")
	idB := PackageStepExecutionSessionID(entry.ID, 2, "two")
	first := addTestAuxiliary(t, manager, &views, entry.ID, idA, false)
	second := addTestAuxiliary(t, manager, &views, entry.ID, idB, false)
	packageWindows[0].rejectClose = true // Models rejection during stage A.
	if packageWindows[0].Close() {
		t.Fatal("rejected package native close succeeded")
	}
	if _, open := registry.Entry(entry.ID); !open || views.Len(entry.ID) != 2 || manager.len() != 3 {
		t.Fatalf("rejected package close changed ownership registry=%v views=%d windows=%d", open, views.Len(entry.ID), manager.len())
	}
	if current, ok := views.Window(entry.ID, idA); !ok || current != first {
		t.Fatal("first auxiliary changed after rejected package close")
	}
	if current, ok := views.Window(entry.ID, idB); !ok || current != second {
		t.Fatal("second auxiliary changed after rejected package close")
	}
}

func TestPackageLifecycleIndependentAuxiliaryCloseAndReopenIsStaleSafe(t *testing.T) {
	registry, manager := newTestDocumentRegistry(t)
	var packageWindows []*fakeDocumentWindow
	entry := openTestPackage(t, registry, &packageWindows)
	var views PackageAuxiliaryViews
	id := PackageStepExecutionSessionID(entry.ID, 1, "one")
	first := addTestAuxiliary(t, manager, &views, entry.ID, id, false)
	stale := first.onClose
	if !first.Close() || views.Len(entry.ID) != 0 || registry.Len() != 1 {
		t.Fatal("independent auxiliary close affected package or membership")
	}
	second := addTestAuxiliary(t, manager, &views, entry.ID, id, false)
	stale()
	if current, ok := views.Window(entry.ID, id); !ok || current != second || registry.Len() != 1 {
		t.Fatal("stale auxiliary callback removed replacement or package")
	}
}

func TestPackageLifecycleDoesNotCrossPackageOwners(t *testing.T) {
	registry, manager := newTestDocumentRegistry(t)
	var windows []*fakeDocumentWindow
	first := openTestPackage(t, registry, &windows)
	secondPath := writeDocument(t, "second.dd", "fixture")
	second, opened, err := registry.OpenPackage(secondPath, func(path string) (*ddpackage.Package, error) { return &ddpackage.Package{SourcePath: path}, nil }, func(app.DocumentID, *ddpackage.Package) DocumentWindow { return &fakeDocumentWindow{} })
	if err != nil || !opened {
		t.Fatalf("second package opened=%v err=%v", opened, err)
	}
	var views PackageAuxiliaryViews
	addTestAuxiliary(t, manager, &views, first.ID, PackageStepExecutionSessionID(first.ID, 1, "one"), false)
	addTestAuxiliary(t, manager, &views, second.ID, PackageStepExecutionSessionID(second.ID, 1, "one"), false)
	if !closeTestPackage(first, &views) || registry.Len() != 1 || views.Len(second.ID) != 1 {
		t.Fatalf("closing first affected second: registry=%d second views=%d", registry.Len(), views.Len(second.ID))
	}
}
