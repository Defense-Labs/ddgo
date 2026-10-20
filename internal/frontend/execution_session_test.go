package frontend

import (
	"errors"
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
