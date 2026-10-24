package frontend

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ianbruene/ddgo/internal/app"
	"github.com/ianbruene/ddgo/internal/gcode"
)

func sessionState(documentID app.DocumentID, runID app.RunID, status app.ProgramStatus, complete int) app.State {
	return app.State{
		ConnectionStatus:  app.ConnectionConnected,
		EStopStatus:       app.EStopClear,
		MachineReadiness:  app.MachineReadinessNormal,
		ProgramDocumentID: documentID,
		ProgramRunID:      runID,
		ProgramStatus:     status,
		ProgramTotal:      2,
		ProgramComplete:   complete,
	}
}

func testSessionProgram() gcode.Program {
	return gcode.Program{Lines: []gcode.Line{{Number: 2, Text: "G0 X1"}, {Number: 5, Text: "G1 X2"}}}
}

func TestExecutionSessionIdentityDistinguishesPackageSteps(t *testing.T) {
	standalone := StandaloneExecutionSessionID(7)
	first := PackageStepExecutionSessionID(7, 1, "step-a")
	second := PackageStepExecutionSessionID(7, 1, "step-b")
	if !standalone.Valid() || !standalone.Standalone() || !first.Valid() || first == second || first.Standalone() {
		t.Fatalf("unexpected identities: standalone=%+v first=%+v second=%+v", standalone, first, second)
	}
	manager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	if _, err := manager.Add(first, "G0 X1", testSessionProgram(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Add(second, "G0 X1", testSessionProgram(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionSessionViewsSharePresentationAndDetach(t *testing.T) {
	s, err := NewExecutionSession(StandaloneExecutionSessionID(7), "G0 X1", testSessionProgram(), nil, sessionState(0, 0, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot().Source; got != "G0 X1" {
		t.Fatalf("retained source = %q", got)
	}
	var first, second []ExecutionSessionSnapshot
	detachFirst := s.Attach(func(snapshot ExecutionSessionSnapshot) { first = append(first, snapshot) })
	detachSecond := s.Attach(func(snapshot ExecutionSessionSnapshot) { second = append(second, snapshot) })
	request, ok := s.BeginStart()
	if !ok || !s.FinishStart(request, 11, nil) {
		t.Fatal("could not admit run")
	}
	s.ApplyState(sessionState(7, 11, app.ProgramRunning, 1))
	if got := first[len(first)-1].Presentation.NextLine; got != 5 {
		t.Fatalf("first next line = %d, want 5", got)
	}
	if got := second[len(second)-1].Presentation.NextLine; got != 5 {
		t.Fatalf("second next line = %d, want 5", got)
	}
	before := len(first)
	detachFirst()
	s.ApplyState(sessionState(7, 11, app.ProgramPaused, 1))
	if len(first) != before || second[len(second)-1].Presentation.Status != app.ProgramPaused {
		t.Fatalf("detach did not isolate observers: first=%d/%d second=%+v", len(first), before, second[len(second)-1])
	}
	detachSecond()
	var reattached ExecutionSessionSnapshot
	s.Attach(func(snapshot ExecutionSessionSnapshot) { reattached = snapshot })
	if reattached.Presentation.RunID != 11 || reattached.Presentation.Status != app.ProgramPaused {
		t.Fatalf("reattach lost retained state: %+v", reattached.Presentation)
	}
}

func TestExecutionSessionSerializesStartAndActions(t *testing.T) {
	s, err := NewExecutionSession(StandaloneExecutionSessionID(7), "G0 X1", testSessionProgram(), nil, sessionState(0, 0, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	request, ok := s.BeginStart()
	if !ok {
		t.Fatal("first start rejected")
	}
	if _, ok := s.BeginStart(); ok {
		t.Fatal("duplicate start accepted")
	}
	if !s.FinishStart(request, 3, nil) {
		t.Fatal("start result rejected")
	}
	s.ApplyState(sessionState(7, 3, app.ProgramRunning, 0))
	if !s.BeginAction(3) || s.BeginAction(3) {
		t.Fatal("actions were not serialized")
	}
	if s.FinishAction(4, nil) || !s.FinishAction(3, errors.New("pause rejected")) {
		t.Fatal("action results did not require exact run")
	}
	if s.Snapshot().Presentation.Error != "pause rejected" {
		t.Fatalf("operation error not retained: %+v", s.Snapshot().Presentation)
	}
}

func TestExecutionSessionsReconcilesStateBeforeAdmissionByExactRun(t *testing.T) {
	initial := sessionState(0, 0, "", 0)
	manager := NewExecutionSessions(initial, 1)
	firstID := PackageStepExecutionSessionID(7, 1, "first")
	secondID := PackageStepExecutionSessionID(7, 1, "second")
	first, _ := manager.Add(firstID, "G0 X1", testSessionProgram(), nil)
	second, _ := manager.Add(secondID, "G0 X1", testSessionProgram(), nil)
	firstRequest, _ := first.BeginStart()
	secondRequest, _ := second.BeginStart()
	// The controller has one DocumentID for both package steps. The event is
	// retained until the exact asynchronous admission result identifies it.
	manager.ApplyEvent(app.Event{StateRevision: 2, State: sessionState(7, 21, app.ProgramRunning, 1)})
	if first.Snapshot().Presentation.RunID != 0 || second.Snapshot().Presentation.RunID != 0 {
		t.Fatal("unassociated state guessed a package-step owner")
	}
	if !manager.AssociateRun(secondID, secondRequest, 21, nil) {
		t.Fatal("second step admission was not associated")
	}
	if got := second.Snapshot().Presentation; got.RunID != 21 || got.NextLine != 5 {
		t.Fatalf("stored state was not replayed to exact step: %+v", got)
	}
	if first.Snapshot().Presentation.RunID != 0 {
		t.Fatal("first step inherited second step run")
	}
	if !manager.AssociateRun(firstID, firstRequest, 22, errors.New("controller busy")) {
		t.Fatal("failed admission was not consumed")
	}
	if first.Snapshot().Presentation.RunID != 0 {
		t.Fatal("failed admission claimed a run")
	}
}

func TestExecutionSessionsRetainOneStepWhileAnotherRuns(t *testing.T) {
	manager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	firstID := PackageStepExecutionSessionID(9, 1, "first")
	secondID := PackageStepExecutionSessionID(9, 1, "second")
	first, _ := manager.Add(firstID, "G0 X1", testSessionProgram(), nil)
	second, _ := manager.Add(secondID, "G0 X1", testSessionProgram(), nil)
	firstRequest, _ := first.BeginStart()
	if !manager.AssociateRun(firstID, firstRequest, 31, nil) {
		t.Fatal("first admission")
	}
	manager.ApplyEvent(app.Event{StateRevision: 2, State: sessionState(9, 31, app.ProgramCompleted, 2)})
	secondRequest, _ := second.BeginStart()
	if !manager.AssociateRun(secondID, secondRequest, 32, nil) {
		t.Fatal("second admission")
	}
	manager.ApplyEvent(app.Event{StateRevision: 3, State: sessionState(9, 32, app.ProgramRunning, 0)})
	if got := first.Snapshot().Presentation; got.RunID != 31 || got.Status != app.ProgramCompleted || got.Complete != 2 || got.NextLine != 0 {
		t.Fatalf("first history was overwritten: %+v", got)
	}
}

func TestExecutionSessionsIgnoreLateResultsAfterRemoval(t *testing.T) {
	manager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	id := StandaloneExecutionSessionID(7)
	session, _ := manager.Add(id, "G0 X1", testSessionProgram(), nil)
	updates := 0
	session.Attach(func(ExecutionSessionSnapshot) { updates++ })
	request, _ := session.BeginStart()
	updatesBeforeRemoval := updates
	manager.Remove(id)
	// Remove clears observers as well as routing ownership, so a retained
	// session pointer cannot keep a destroyed view alive through callbacks.
	session.ApplyState(sessionState(7, 44, app.ProgramRunning, 0))
	if updates != updatesBeforeRemoval {
		t.Fatal("removed session retained an observer")
	}
	if manager.AssociateRun(id, request, 44, nil) {
		t.Fatal("late result updated removed session")
	}
	if _, err := manager.Add(StandaloneExecutionSessionID(8), "G0 X1", testSessionProgram(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionSessionControlsRequireExactPackageStepRun(t *testing.T) {
	manager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	stepAID := PackageStepExecutionSessionID(7, 1, "step-a")
	stepBID := PackageStepExecutionSessionID(7, 1, "step-b")
	stepA, _ := manager.Add(stepAID, "G0 X1", testSessionProgram(), nil)
	stepB, _ := manager.Add(stepBID, "G0 X1", testSessionProgram(), nil)

	// Retain an earlier result for B, then run A under the same package owner.
	requestB, ok := manager.BeginStart(stepBID)
	if !ok || !manager.AssociateRun(stepBID, requestB, 10, nil) {
		t.Fatal("could not establish B history")
	}
	manager.ApplyEvent(app.Event{StateRevision: 2, State: sessionState(7, 10, app.ProgramCompleted, 2)})
	requestA, ok := manager.BeginStart(stepAID)
	if !ok || !manager.AssociateRun(stepAID, requestA, 11, nil) {
		t.Fatal("could not start A")
	}
	manager.ApplyEvent(app.Event{StateRevision: 3, State: sessionState(7, 11, app.ProgramRunning, 0)})

	if controls := stepA.Snapshot().Controls; !controls.Pause || !controls.Stop || controls.Resume || controls.Run {
		t.Fatalf("A controls = %+v", controls)
	}
	if controls := stepB.Snapshot().Controls; controls.Pause || controls.Resume || controls.Stop || controls.Run {
		t.Fatalf("B borrowed A controls: %+v", controls)
	}
	if got := stepB.Snapshot().Presentation; got.RunID != 10 || got.Status != app.ProgramCompleted {
		t.Fatalf("B history was not retained: %+v", got)
	}
	if stepB.BeginAction(11) || stepB.BeginAction(10) {
		t.Fatal("B accepted an action for a stale or sibling run")
	}

	otherID := PackageStepExecutionSessionID(8, 1, "step-a")
	other, _ := manager.Add(otherID, "G0 X1", testSessionProgram(), nil)
	if controls := other.Snapshot().Controls; controls.Pause || controls.Resume || controls.Stop {
		t.Fatalf("different package inherited controls: %+v", controls)
	}
}

func TestExecutionSessionSnapshotsOwnProgramAndObserverCopies(t *testing.T) {
	program := testSessionProgram()
	program.Path, program.Name = "Code/fixture.nc", "fixture"
	session, err := NewExecutionSession(StandaloneExecutionSessionID(7), "G0 X1\nG1 X2", program, nil, sessionState(0, 0, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	program.Lines[0].Text = "MUTATED CALLER"
	if got := session.Snapshot().Program.Lines[0].Text; got != "G0 X1" {
		t.Fatalf("caller mutation reached session: %q", got)
	}

	snapshot := session.Snapshot()
	snapshot.Program.Lines[0].Text = "MUTATED SNAPSHOT"
	snapshot.Presentation.lines[0].Number = 999
	later := session.Snapshot()
	if got := later.Program.Lines[0].Text; got != "G0 X1" {
		t.Fatalf("snapshot mutation reached session: %q", got)
	}
	if got := later.Presentation.lines[0].Number; got != 2 {
		t.Fatalf("presentation line mutation reached session: %d", got)
	}

	var second ExecutionSessionSnapshot
	session.Attach(func(got ExecutionSessionSnapshot) {
		got.Program.Lines[0].Text = "MUTATED OBSERVER"
		got.Presentation.lines[0].Number = 123
	})
	session.Attach(func(got ExecutionSessionSnapshot) { second = got })
	request, _ := session.BeginStart()
	session.FinishStart(request, 11, nil)
	session.ApplyState(sessionState(7, 11, app.ProgramRunning, 1))
	if got := second.Program.Lines[0].Text; got != "G0 X1" || second.Presentation.NextLine != 5 {
		t.Fatalf("observer snapshot aliased another observer: program=%q presentation=%+v", got, second.Presentation)
	}
	var reattached ExecutionSessionSnapshot
	session.Attach(func(got ExecutionSessionSnapshot) { reattached = got })
	if reattached.Program.Path != "Code/fixture.nc" || reattached.Program.Name != "fixture" || reattached.Presentation.NextLine != 5 {
		t.Fatalf("reattached snapshot lost retained data: %+v", reattached)
	}
}

func TestExecutionSessionsBufferedReplayPreservesNewerGlobalState(t *testing.T) {
	manager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	stepAID := PackageStepExecutionSessionID(7, 1, "step-a")
	stepBID := PackageStepExecutionSessionID(7, 1, "step-b")
	stepA, _ := manager.Add(stepAID, "G0 X1", testSessionProgram(), nil)
	stepB, _ := manager.Add(stepBID, "G0 X1", testSessionProgram(), nil)
	request, ok := manager.BeginStart(stepAID)
	if !ok {
		t.Fatal("start was not reserved")
	}
	manager.ApplyEvent(app.Event{StateRevision: 20, State: sessionState(7, 41, app.ProgramRunning, 1)})
	newer := sessionState(0, 0, "", 0)
	newer.ConnectionStatus = app.ConnectionDisconnected
	newer.MachineReadiness = app.MachineReadinessRecoveryRequired
	manager.ApplyEvent(app.Event{StateRevision: 21, State: newer})
	if !manager.AssociateRun(stepAID, request, 41, nil) {
		t.Fatal("admission result was rejected")
	}
	got := stepA.Snapshot()
	if got.Presentation.RunID != 41 || got.Presentation.Complete != 1 || got.Presentation.NextLine != 5 {
		t.Fatalf("buffered progress was not retained: %+v", got.Presentation)
	}
	if got.State.ConnectionStatus != app.ConnectionDisconnected || got.State.MachineReadiness != app.MachineReadinessRecoveryRequired {
		t.Fatalf("buffered state rolled back global controls: %+v", got.State)
	}
	if got.Controls.Run || got.Controls.Pause || got.Controls.Resume || got.Controls.Stop {
		t.Fatalf("controls used stale global state: %+v", got.Controls)
	}
	if stepB.Snapshot().Presentation.RunID != 0 {
		t.Fatal("sibling inherited buffered run")
	}
	// An out-of-order state remains ignored by the manager revision guard.
	manager.ApplyEvent(app.Event{StateRevision: 19, State: sessionState(7, 41, app.ProgramCompleted, 2)})
	if got := stepA.Snapshot().State; got.ConnectionStatus != app.ConnectionDisconnected {
		t.Fatalf("older event replaced newer global state: %+v", got)
	}

	terminalManager := NewExecutionSessions(sessionState(0, 0, "", 1), 1)
	terminalID := PackageStepExecutionSessionID(12, 1, "terminal")
	terminal, _ := terminalManager.Add(terminalID, "G0 X1", testSessionProgram(), nil)
	terminalRequest, _ := terminalManager.BeginStart(terminalID)
	failed := sessionState(12, 71, app.ProgramFailed, 1)
	failed.ProgramError = "state failure"
	terminalManager.ApplyEvent(app.Event{StateRevision: 30, State: failed})
	terminalManager.ApplyEvent(app.Event{Kind: app.EventError, StateRevision: 30, ErrorDocumentID: 12, ErrorRunID: 71, Err: errors.New("event failure")})
	latest := sessionState(0, 0, "", 0)
	latest.MachineReadiness = app.MachineReadinessRecoveryRequired
	terminalManager.ApplyEvent(app.Event{StateRevision: 31, State: latest})
	if !terminalManager.AssociateRun(terminalID, terminalRequest, 71, nil) {
		t.Fatal("terminal admission was rejected")
	}
	if got := terminal.Snapshot(); got.Presentation.Status != app.ProgramFailed || got.Presentation.Error != "event failure" || got.State.MachineReadiness != app.MachineReadinessRecoveryRequired {
		t.Fatalf("terminal replay was not exact or rolled back global state: %+v", got)
	}
}

func TestExecutionSessionsHoldRunAdmissionUntilAuthoritativeState(t *testing.T) {
	manager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	stepAID := PackageStepExecutionSessionID(7, 1, "step-a")
	stepBID := PackageStepExecutionSessionID(7, 1, "step-b")
	stepA, _ := manager.Add(stepAID, "G0 X1", testSessionProgram(), nil)
	_, _ = manager.Add(stepBID, "G0 X1", testSessionProgram(), nil)

	request, ok := manager.BeginStart(stepAID)
	if !ok || !manager.AssociateRun(stepAID, request, 51, nil) {
		t.Fatal("first admission failed")
	}
	if _, accepted := stepA.BeginStart(); stepA.Snapshot().Controls.Run || accepted {
		t.Fatal("Run reopened before authoritative state")
	}
	if _, ok := manager.BeginStart(stepBID); ok {
		t.Fatal("sibling package step admitted while A was unresolved")
	}
	// Another document's event cannot settle A's admission.
	manager.ApplyEvent(app.Event{StateRevision: 2, State: sessionState(8, 61, app.ProgramRunning, 0)})
	if _, accepted := stepA.BeginStart(); stepA.Snapshot().Controls.Run || accepted {
		t.Fatal("other document state released A admission")
	}
	manager.ApplyEvent(app.Event{StateRevision: 3, State: sessionState(7, 51, app.ProgramRunning, 0)})
	if _, accepted := stepA.BeginStart(); stepA.Snapshot().Controls.Run || accepted {
		t.Fatal("active run made duplicate admission possible")
	}
	manager.ApplyEvent(app.Event{StateRevision: 4, State: sessionState(7, 51, app.ProgramCompleted, 2)})
	if _, ok := manager.BeginStart(stepAID); !ok {
		t.Fatal("completed authoritative run did not permit a later run")
	}

	terminalManager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	terminalID := PackageStepExecutionSessionID(10, 1, "terminal")
	terminal, _ := terminalManager.Add(terminalID, "G0 X1", testSessionProgram(), nil)
	terminalRequest, _ := terminalManager.BeginStart(terminalID)
	terminalManager.ApplyEvent(app.Event{StateRevision: 2, State: sessionState(10, 71, app.ProgramCompleted, 2)})
	if !terminalManager.AssociateRun(terminalID, terminalRequest, 71, nil) {
		t.Fatal("buffered terminal admission failed")
	}
	if got := terminal.Snapshot(); got.Presentation.Status != app.ProgramCompleted || got.Controls.Run != true {
		t.Fatalf("buffered terminal state did not resolve admission: %+v", got)
	}
	if got, ok := terminalManager.PackageExecution(10); !ok || got.Phase != PackageExecutionHistorical || got.ID != terminalID {
		t.Fatalf("buffered terminal package projection = %+v, %v", got, ok)
	}

	standalone, err := NewExecutionSession(StandaloneExecutionSessionID(9), "G0 X1", testSessionProgram(), nil, sessionState(0, 0, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	failedRequest, _ := standalone.BeginStart()
	if !standalone.FinishStart(failedRequest, 0, errors.New("controller busy")) {
		t.Fatal("failed admission was not consumed")
	}
	if _, ok := standalone.BeginStart(); !ok {
		t.Fatal("rejected admission did not permit retry")
	}
}

func TestExecutionSessionsImmediatelyPropagatePackageAdmissionEligibility(t *testing.T) {
	manager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	stepAID := PackageStepExecutionSessionID(7, 1, "step-a")
	stepBID := PackageStepExecutionSessionID(7, 1, "step-b")
	stepA, _ := manager.Add(stepAID, "G0 X1", testSessionProgram(), nil)
	stepB, _ := manager.Add(stepBID, "G0 X1", testSessionProgram(), nil)
	var aUpdates, bUpdates []ExecutionSessionSnapshot
	stepA.Attach(func(snapshot ExecutionSessionSnapshot) { aUpdates = append(aUpdates, snapshot) })
	detachB := stepB.Attach(func(snapshot ExecutionSessionSnapshot) { bUpdates = append(bUpdates, snapshot) })
	if !stepA.Snapshot().Controls.Run || !stepB.Snapshot().Controls.Run {
		t.Fatalf("initial package controls = A:%+v B:%+v", stepA.Snapshot().Controls, stepB.Snapshot().Controls)
	}

	request, ok := manager.BeginStart(stepAID)
	if !ok {
		t.Fatal("A admission was rejected")
	}
	if stepA.Snapshot().Controls.Run || stepB.Snapshot().Controls.Run {
		t.Fatalf("pending admission did not disable siblings: A:%+v B:%+v", stepA.Snapshot().Controls, stepB.Snapshot().Controls)
	}
	if len(aUpdates) < 2 || len(bUpdates) < 2 || bUpdates[len(bUpdates)-1].Controls.Run {
		t.Fatalf("pending admission did not notify both views: A=%d B=%d lastB=%+v", len(aUpdates), len(bUpdates), bUpdates[len(bUpdates)-1].Controls)
	}
	if _, ok := manager.BeginStart(stepBID); ok {
		t.Fatal("manager accepted sibling while A admission was pending")
	}

	updatesBeforeSuccess := len(bUpdates)
	if !manager.AssociateRun(stepAID, request, 51, nil) {
		t.Fatal("A admission result was rejected")
	}
	if stepA.Snapshot().Controls.Run || stepB.Snapshot().Controls.Run {
		t.Fatalf("successful admission reopened Run before controller state: A:%+v B:%+v", stepA.Snapshot().Controls, stepB.Snapshot().Controls)
	}
	if len(bUpdates) <= updatesBeforeSuccess || bUpdates[len(bUpdates)-1].Controls.Run {
		t.Fatalf("success transition did not notify sibling: updates=%d controls=%+v", len(bUpdates), bUpdates[len(bUpdates)-1].Controls)
	}
	if _, ok := manager.BeginStart(stepBID); ok {
		t.Fatal("manager accepted sibling while A awaited controller state")
	}

	manager.ApplyEvent(app.Event{StateRevision: 2, State: sessionState(7, 51, app.ProgramRunning, 0)})
	if stepA.Snapshot().Controls.Run || stepB.Snapshot().Controls.Run {
		t.Fatalf("running state reopened Run: A:%+v B:%+v", stepA.Snapshot().Controls, stepB.Snapshot().Controls)
	}
	manager.ApplyEvent(app.Event{StateRevision: 3, State: sessionState(7, 51, app.ProgramCompleted, 2)})
	if !stepA.Snapshot().Controls.Run || !stepB.Snapshot().Controls.Run {
		t.Fatalf("terminal state did not restore package eligibility: A:%+v B:%+v", stepA.Snapshot().Controls, stepB.Snapshot().Controls)
	}
	if got := stepA.Snapshot().Presentation; got.RunID != 51 || got.Status != app.ProgramCompleted {
		t.Fatalf("terminal history was not retained: %+v", got)
	}

	detachB()
	updatesBeforeDetach := len(bUpdates)
	request, ok = manager.BeginStart(stepAID)
	if !ok {
		t.Fatal("later A admission was rejected")
	}
	if !manager.AssociateRun(stepAID, request, 0, errors.New("controller busy")) {
		t.Fatal("rejected A admission was not consumed")
	}
	if len(bUpdates) != updatesBeforeDetach {
		t.Fatal("detached sibling received a notification")
	}
	var reattached ExecutionSessionSnapshot
	stepB.Attach(func(snapshot ExecutionSessionSnapshot) { reattached = snapshot })
	if !reattached.Controls.Run {
		t.Fatalf("reattached sibling did not recover eligibility: %+v", reattached.Controls)
	}
}

func TestExecutionSessionsScopeAdmissionLocksToPackageOwner(t *testing.T) {
	manager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	packageA := PackageStepExecutionSessionID(7, 1, "step-a")
	packageASibling := PackageStepExecutionSessionID(7, 1, "step-b")
	packageB := PackageStepExecutionSessionID(8, 1, "step-a")
	standaloneA := StandaloneExecutionSessionID(9)
	standaloneB := StandaloneExecutionSessionID(10)
	_, _ = manager.Add(packageA, "G0 X1", testSessionProgram(), nil)
	sibling, _ := manager.Add(packageASibling, "G0 X1", testSessionProgram(), nil)
	otherPackage, _ := manager.Add(packageB, "G0 X1", testSessionProgram(), nil)
	standaloneOne, _ := manager.Add(standaloneA, "G0 X1", testSessionProgram(), nil)
	standaloneTwo, _ := manager.Add(standaloneB, "G0 X1", testSessionProgram(), nil)

	if _, ok := manager.BeginStart(packageA); !ok {
		t.Fatal("package A admission was rejected")
	}
	if sibling.Snapshot().Controls.Run {
		t.Fatal("same-package sibling remained enabled")
	}
	if !otherPackage.Snapshot().Controls.Run || !standaloneOne.Snapshot().Controls.Run || !standaloneTwo.Snapshot().Controls.Run {
		t.Fatalf("unrelated owners inherited package lock: package=%+v standaloneA=%+v standaloneB=%+v", otherPackage.Snapshot().Controls, standaloneOne.Snapshot().Controls, standaloneTwo.Snapshot().Controls)
	}
}

func TestExecutionSessionsKeepSiblingControlsLockedWhenStatePrecedesAdmission(t *testing.T) {
	manager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	stepAID := PackageStepExecutionSessionID(7, 1, "step-a")
	stepBID := PackageStepExecutionSessionID(7, 1, "step-b")
	_, _ = manager.Add(stepAID, "G0 X1", testSessionProgram(), nil)
	stepB, _ := manager.Add(stepBID, "G0 X1", testSessionProgram(), nil)
	request, ok := manager.BeginStart(stepAID)
	if !ok || stepB.Snapshot().Controls.Run {
		t.Fatal("pending admission did not lock sibling")
	}
	// The state is accepted before the asynchronous Start result identifies A.
	manager.ApplyEvent(app.Event{StateRevision: 3, State: sessionState(7, 73, app.ProgramRunning, 0)})
	if stepB.Snapshot().Controls.Run {
		t.Fatal("pre-admission running state released sibling")
	}
	if !manager.AssociateRun(stepAID, request, 73, nil) || stepB.Snapshot().Controls.Run {
		t.Fatal("reconciliation released sibling while the run is active")
	}
	// A stale revision cannot change either the manager's state or lock.
	manager.ApplyEvent(app.Event{StateRevision: 2, State: sessionState(0, 0, "", 0)})
	if stepB.Snapshot().Controls.Run {
		t.Fatal("stale state released sibling")
	}
}

func TestExecutionSessionsPackageExecutionUsesExactStepNotSelection(t *testing.T) {
	manager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	stepAID := PackageStepExecutionSessionID(7, 1, "step-a")
	stepBID := PackageStepExecutionSessionID(7, 1, "step-b")
	stepA, _ := manager.Add(stepAID, "G0 X1", testSessionProgram(), nil)
	_, _ = manager.Add(stepBID, "G0 X2", testSessionProgram(), nil)

	request, ok := manager.BeginStart(stepAID)
	if !ok {
		t.Fatal("start admission rejected")
	}
	if snapshot, ok := manager.PackageExecution(7); !ok || snapshot.ID != stepAID || snapshot.Phase != PackageExecutionStartPending {
		t.Fatalf("pending package execution = %+v, %v", snapshot, ok)
	}
	if !manager.AssociateRun(stepAID, request, 41, nil) {
		t.Fatal("could not associate run")
	}
	manager.ApplyEvent(app.Event{StateRevision: 2, State: sessionState(7, 41, app.ProgramRunning, 1)})
	if snapshot, ok := manager.PackageExecution(7); !ok || snapshot.ID != stepAID || snapshot.Presentation.RunID != 41 || snapshot.Presentation.Status != app.ProgramRunning {
		t.Fatalf("active package execution = %+v, %v", snapshot, ok)
	}
	manager.ApplyEvent(app.Event{StateRevision: 3, State: sessionState(7, 41, app.ProgramCompleted, 2)})
	if snapshot, ok := manager.PackageExecution(7); !ok || snapshot.ID != stepAID || snapshot.Presentation.Status != app.ProgramCompleted {
		t.Fatalf("historical package execution = %+v, %v", snapshot, ok)
	}
	if stepA.Snapshot().Presentation.RunID != 41 {
		t.Fatal("source session lost its retained run")
	}
	if _, ok := manager.PackageExecution(8); ok {
		t.Fatal("execution leaked to another package owner")
	}
}

func TestExecutionSessionsRetainRejectedPackageAttemptAndPriorRun(t *testing.T) {
	manager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	stepID := PackageStepExecutionSessionID(7, 1, "machining")
	step, _ := manager.Add(stepID, "G0 X1", testSessionProgram(), nil)

	request, ok := manager.BeginStart(stepID)
	if !ok || !manager.AssociateRun(stepID, request, 31, nil) {
		t.Fatal("first admission failed")
	}
	manager.ApplyEvent(app.Event{StateRevision: 2, State: sessionState(7, 31, app.ProgramCompleted, 2)})
	request, ok = manager.BeginStart(stepID)
	if !ok {
		t.Fatal("repeat admission was rejected before controller result")
	}
	if !manager.AssociateRun(stepID, request, 0, errors.New("controller is not ready")) {
		t.Fatal("rejected result was not accepted")
	}
	got, ok := manager.PackageExecution(7)
	if !ok || got.Phase != PackageExecutionStartRejected || got.ID != stepID || got.AttemptErr == nil || got.AttemptErr.Error() != "controller is not ready" {
		t.Fatalf("rejected attempt projection = %+v, %v", got, ok)
	}
	if got.Controls.Pause || got.Controls.Resume || got.Controls.Stop {
		t.Fatalf("rejected attempt exposed actions: %+v", got.Controls)
	}
	if retained := step.Snapshot().Presentation; retained.RunID != 31 || retained.Status != app.ProgramCompleted {
		t.Fatalf("rejected repeat destroyed successful history: %+v", retained)
	}
	newRequest, ok := manager.BeginStart(stepID)
	if !ok {
		t.Fatal("new attempt was not admitted after rejection")
	}
	if manager.AssociateRun(stepID, request, 0, errors.New("stale rejection")) {
		t.Fatal("stale result was accepted for the newer attempt")
	}
	if got, ok := manager.PackageExecution(7); !ok || got.Phase != PackageExecutionStartPending || got.AttemptSequence <= 1 || got.AttemptErr != nil {
		t.Fatalf("stale rejection overwrote newer attempt: %+v, %v", got, ok)
	}
	if !manager.AssociateRun(stepID, newRequest, 32, nil) {
		t.Fatal("new successful admission was rejected")
	}
}

func TestExecutionSessionsPackageObserverIsOwnerScoped(t *testing.T) {
	manager := NewExecutionSessions(sessionState(0, 0, "", 0), 1)
	for i := 0; i < 100; i++ {
		id := PackageStepExecutionSessionID(7, 1, fmt.Sprintf("step-%d", i))
		if _, err := manager.Add(id, "G0 X1", testSessionProgram(), nil); err != nil {
			t.Fatal(err)
		}
	}
	updates := 0
	detach := manager.AttachPackage(7, func(PackageExecutionPresentation) { updates++ })
	stepID := PackageStepExecutionSessionID(7, 1, "step-0")
	request, ok := manager.BeginStart(stepID)
	if !ok || !manager.AssociateRun(stepID, request, 51, nil) {
		t.Fatal("could not prepare active run")
	}
	updates = 0
	manager.ApplyEvent(app.Event{StateRevision: 2, State: sessionState(7, 51, app.ProgramRunning, 1)})
	if updates != 1 {
		t.Fatalf("package observer updates = %d, want one regardless of retained steps", updates)
	}
	detach()
	manager.ApplyEvent(app.Event{StateRevision: 3, State: sessionState(7, 51, app.ProgramCompleted, 2)})
	if updates != 1 {
		t.Fatalf("detached package observer received %d updates", updates)
	}
}
