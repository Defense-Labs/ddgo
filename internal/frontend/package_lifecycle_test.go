package frontend

import (
	"errors"
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/ddpackage"
	"github.com/ianbruene/ddgo/internal/gcode"
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

func TestPackageLifecycleDeferredRejectedAdmissionClosesOnlyItsPackage(t *testing.T) {
	registry, windows := newTestDocumentRegistry(t)
	var packageWindows []*fakeDocumentWindow
	entry := openTestPackage(t, registry, &packageWindows)
	otherPath := writeDocument(t, "other.gcode", "G0 X1\n")
	other, opened, err := registry.OpenFile(otherPath, gcode.LoadDocument, testDocumentWindowFactory(new([]*fakeDocumentWindow)))
	if err != nil || !opened {
		t.Fatalf("open unrelated document = %+v, %v, %v", other, opened, err)
	}

	sessions := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	stepA := PackageStepExecutionSessionID(entry.ID, 1, "a")
	stepB := PackageStepExecutionSessionID(entry.ID, 1, "b")
	if _, err := sessions.Add(stepA, "G0 X1", testSessionProgram(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Add(stepB, "G0 X1", testSessionProgram(), nil); err != nil {
		t.Fatal(err)
	}
	var views PackageAuxiliaryViews
	addTestAuxiliary(t, windows, &views, entry.ID, stepA, false)
	addTestAuxiliary(t, windows, &views, entry.ID, stepB, false)
	registry.SetOnClose(func(closed DocumentEntry) {
		if closed.ID != entry.ID || views.Len(closed.ID) != 0 {
			return
		}
		sessions.Remove(stepA)
		sessions.Remove(stepB)
	})

	request, ok := sessions.BeginStart(stepA)
	if !ok || !registry.MarkClosePending(entry.ID) {
		t.Fatal("could not establish deferred package close")
	}
	if registry.ClosePending(other.ID) || views.Len(entry.ID) != 2 || windows.len() != 4 {
		t.Fatalf("deferred close changed unrelated ownership: otherPending=%v views=%d windows=%d", registry.ClosePending(other.ID), views.Len(entry.ID), windows.len())
	}
	if !sessions.AssociateRun(stepA, request, 0, errors.New("controller rejected Start")) {
		t.Fatal("rejected admission was not reconciled")
	}
	if got := ResolveDeferredAdmissionClose(registry.ClosePending(entry.ID), 0, errors.New("controller rejected Start"), false); got != DeferredAdmissionCloseRetryNormal {
		t.Fatalf("rejected deferred admission disposition = %v", got)
	}
	registry.ClearClosePending(entry.ID)
	if !closeTestPackage(entry, &views) {
		t.Fatal("normal package close did not resume after rejected admission")
	}
	if _, open := registry.Entry(entry.ID); open || views.Len(entry.ID) != 0 || windows.len() != 1 {
		t.Fatalf("package ownership was not cleaned up: open=%v views=%d windows=%d", open, views.Len(entry.ID), windows.len())
	}
	if _, exists := sessions.Session(stepA); exists {
		t.Fatal("accepted package close retained step session")
	}
	if retained, open := registry.Entry(other.ID); !open || retained.ID != other.ID {
		t.Fatalf("unrelated document was affected by package close: %+v, %v", retained, open)
	}
}

func TestPackageLifecycleVerifiedStopRequiresExactRunAndAuxiliaryTeardown(t *testing.T) {
	registry, windows := newTestDocumentRegistry(t)
	var packageWindows []*fakeDocumentWindow
	entry := openTestPackage(t, registry, &packageWindows)
	step := PackageStepExecutionSessionID(entry.ID, 1, "a")
	var views PackageAuxiliaryViews
	addTestAuxiliary(t, windows, &views, entry.ID, step, false)

	running := sessionState(entry.ID, 51, app.ProgramRunning, 1)
	if got := ClassifyDocumentStopResult(nil, true, entry.ID, 51, running, false); got != DocumentStopInvalid {
		t.Fatalf("active run was closeable: %v", got)
	}
	draining := running
	draining.ProgramStatus = app.ProgramDraining
	if got := ClassifyDocumentStopResult(nil, true, entry.ID, 51, draining, false); got != DocumentStopInvalid {
		t.Fatalf("draining run was closeable: %v", got)
	}
	if views.Len(entry.ID) != 1 || registry.Len() != 1 {
		t.Fatal("invalid Stop result changed package ownership")
	}

	stopped := stoppedDocumentState(entry.ID, 51)
	if got := ClassifyDocumentStopResult(nil, true, entry.ID, 52, stopped, false); got != DocumentStopInvalid {
		t.Fatalf("wrong RunID was accepted: %v", got)
	}
	if got := ClassifyDocumentStopResult(nil, true, entry.ID, 51, stopped, false); got != DocumentStopCloseIndependently {
		t.Fatalf("verified exact Stop was not approved: %v", got)
	}
	approvals := DocumentCloseApprovals{entry.ID: 51}
	if approvals.InvalidateSuperseded(registry, entry.ID, running) {
		t.Fatal("own RunID superseded its approval")
	}
	if !closeTestPackage(entry, &views) || registry.Len() != 0 || views.Len(entry.ID) != 0 || windows.len() != 0 {
		t.Fatalf("verified Stop did not permit owned teardown: registry=%d views=%d windows=%d", registry.Len(), views.Len(entry.ID), windows.len())
	}
}

func TestPackageLifecycleShutdownClosesOneLogicalDocumentAfterAuxiliaries(t *testing.T) {
	registry, windows := newTestDocumentRegistry(t)
	var packageWindows []*fakeDocumentWindow
	entry := openTestPackage(t, registry, &packageWindows)
	var views PackageAuxiliaryViews
	addTestAuxiliary(t, windows, &views, entry.ID, PackageStepExecutionSessionID(entry.ID, 1, "a"), false)
	addTestAuxiliary(t, windows, &views, entry.ID, PackageStepExecutionSessionID(entry.ID, 1, "b"), false)

	entries := registry.Entries()
	if len(entries) != 1 || entries[0].ID != entry.ID {
		t.Fatalf("auxiliaries became shutdown documents: %+v", entries)
	}
	var shutdown ShutdownCoordinator
	attempt, ok := shutdown.Begin([]app.DocumentID{entry.ID})
	if !ok || shutdown.ConfirmApplication(attempt, true) != ShutdownWaitingForRunAdmission ||
		shutdown.ResolveRunAdmission(attempt, app.State{MachineReadiness: app.MachineReadinessNormal}) != ShutdownClosingDocuments {
		t.Fatalf("shutdown did not reach document closure: phase=%v", shutdown.Phase())
	}
	id, ok := shutdown.NextDocumentToClose(attempt)
	if !ok || id != entry.ID || !shutdown.CanCloseSelectedDocument(attempt, id, app.State{MachineReadiness: app.MachineReadinessNormal}) {
		t.Fatalf("package was not the one authorized shutdown document: %d, %v", id, ok)
	}
	if !closeTestPackage(entry, &views) || !shutdown.DocumentClosed(attempt, id) {
		t.Fatal("accepted auxiliary teardown did not close package through shutdown")
	}
	if registry.Len() != 0 || windows.len() != 0 || views.Len(entry.ID) != 0 {
		t.Fatalf("successful shutdown teardown retained ownership: registry=%d windows=%d views=%d", registry.Len(), windows.len(), views.Len(entry.ID))
	}
	if _, ok := shutdown.NextDocumentToClose(attempt); ok || shutdown.Phase() != ShutdownReadyToCloseMain || !shutdown.CommitMainClose(attempt) {
		t.Fatalf("shutdown did not become safe for main close: %v", shutdown.Phase())
	}
}

func TestPackageLifecycleShutdownCancelsWhenAuxiliaryRefusesClose(t *testing.T) {
	registry, windows := newTestDocumentRegistry(t)
	var packageWindows []*fakeDocumentWindow
	entry := openTestPackage(t, registry, &packageWindows)
	var views PackageAuxiliaryViews
	addTestAuxiliary(t, windows, &views, entry.ID, PackageStepExecutionSessionID(entry.ID, 1, "a"), true)

	var shutdown ShutdownCoordinator
	attempt, _ := shutdown.Begin([]app.DocumentID{entry.ID})
	shutdown.ConfirmApplication(attempt, true)
	shutdown.ResolveRunAdmission(attempt, app.State{MachineReadiness: app.MachineReadinessNormal})
	id, ok := shutdown.NextDocumentToClose(attempt)
	if !ok || id != entry.ID || closeTestPackage(entry, &views) {
		t.Fatal("refusing auxiliary was accepted as package teardown")
	}
	if !shutdown.DocumentCloseRejected(attempt, id) || shutdown.Phase() != ShutdownIdle {
		t.Fatalf("refused package close did not cancel shutdown: %v", shutdown.Phase())
	}
	if registry.Len() != 1 || windows.len() != 2 || views.Len(entry.ID) != 1 {
		t.Fatalf("rejected shutdown lost package ownership: registry=%d windows=%d views=%d", registry.Len(), windows.len(), views.Len(entry.ID))
	}
}
