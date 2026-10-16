package frontend

import "testing"

func TestLegacyProgramCoordinatorSerializesLoadAndRun(t *testing.T) {
	var c LegacyProgramCoordinator
	loadID, ok := c.BeginLoad()
	if !ok || !c.LoadPending() {
		t.Fatal("load was not registered")
	}
	if _, ok := c.BeginLoad(); ok {
		t.Fatal("overlapping load was accepted")
	}
	if _, ok := c.BeginRun(); ok {
		t.Fatal("run overtook pending load")
	}
	if !c.FinishLoad(loadID) || c.LoadPending() {
		t.Fatal("load did not finish")
	}
	runID, ok := c.BeginRun()
	if !ok || !c.RunPending() {
		t.Fatal("run was not registered")
	}
	if _, ok := c.BeginLoad(); ok {
		t.Fatal("load overtook accepted run")
	}
	if c.FinishRun(runID+1) || !c.RunPending() {
		t.Fatal("stale run result changed pending run")
	}
	if !c.FinishRun(runID) || c.RunPending() {
		t.Fatal("run did not finish")
	}
}
