package frontend

import "testing"

func TestProgramStartGateAccountsAdmissionsAndDuplicateRelease(t *testing.T) {
	var gate ProgramStartGate
	first, ok := gate.Acquire()
	if !ok || gate.Outstanding() != 1 {
		t.Fatalf("first admission = %v, outstanding = %d", ok, gate.Outstanding())
	}
	second, ok := gate.Acquire()
	if !ok || gate.Outstanding() != 2 {
		t.Fatalf("second admission = %v, outstanding = %d", ok, gate.Outstanding())
	}
	second.Release()
	if gate.Outstanding() != 1 || gate.Quiescent() {
		t.Fatalf("outstanding after second release = %d", gate.Outstanding())
	}
	first.Release()
	first.Release()
	if !gate.Quiescent() || gate.Outstanding() != 0 {
		t.Fatalf("duplicate release corrupted outstanding count: %d", gate.Outstanding())
	}
}

func TestProgramStartGateFreezeTracksEarlierAdmission(t *testing.T) {
	var gate ProgramStartGate
	admission, ok := gate.Acquire() // synchronous UI callback admission
	if !ok {
		t.Fatal("initial admission rejected")
	}
	started := make(chan struct{})
	releaseControllerStart := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		close(started) // controller-start goroutine exists but has not admitted
		<-releaseControllerStart
		admission.Release()
		close(finished)
	}()
	<-started
	gate.Freeze()
	if _, ok := gate.Acquire(); ok {
		t.Fatal("new admission succeeded after shutdown froze gate")
	}
	if gate.Quiescent() {
		t.Fatal("delayed controller goroutine was not visible as outstanding")
	}
	close(releaseControllerStart)
	<-finished
	if !gate.Quiescent() {
		t.Fatal("gate did not become quiescent after controller admission returned")
	}
}

func TestProgramStartGateReopenAndFinalization(t *testing.T) {
	var gate ProgramStartGate
	gate.Freeze()
	if _, ok := gate.Acquire(); ok {
		t.Fatal("frozen gate accepted a request")
	}
	gate.Reopen()
	admission, ok := gate.Acquire()
	if !ok {
		t.Fatal("cancelled shutdown did not reopen admission")
	}
	admission.Release()
	gate.Finalize()
	gate.Reopen()
	if _, ok := gate.Acquire(); ok {
		t.Fatal("finalized gate reopened")
	}
}

func TestProgramStartGateOldReleaseCannotReopenNewFreeze(t *testing.T) {
	var gate ProgramStartGate
	admission, ok := gate.Acquire()
	if !ok {
		t.Fatal("initial admission rejected")
	}
	gate.Freeze() // shutdown attempt A
	gate.Reopen() // A cancelled
	gate.Freeze() // shutdown attempt B
	admission.Release()
	if _, ok := gate.Acquire(); ok {
		t.Fatal("old request completion reopened a newer shutdown attempt")
	}
}
