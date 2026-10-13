package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ianbruene/ddgo/internal/transport"
)

func TestConnectCommitsNumericGRBLSettingsSnapshot(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)
	done := startManualHandshake(t, tr, c)
	completeManualHandshake(t, tr, 1,
		"$24=30.000 (homeFine)",
		"$25=2000.000 (homeSeek)",
		"$26=1 (homeDelay)",
		"$27=0.500 (homePullOff)",
		"$130=86.500 (x:mm)",
		"$131=241.500 (y:mm)",
		"$132=78.500 (z:mm)",
		"ok",
	)
	if err := <-done; err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	snapshot := c.grblSettingsSnapshotCopy()
	if snapshot.generation != 1 {
		t.Fatalf("settings generation = %d, want 1", snapshot.generation)
	}
	want := map[int]float64{
		24:  30,
		25:  2000,
		26:  1,
		27:  0.5,
		130: 86.5,
		131: 241.5,
		132: 78.5,
	}
	if len(snapshot.values) != len(want) {
		t.Fatalf("settings = %#v, want %#v", snapshot.values, want)
	}
	for number, value := range want {
		if got := snapshot.values[number]; got != value {
			t.Errorf("setting $%d = %v, want %v", number, got, value)
		}
	}

	// Callers receive detached data that can be handed to pure policy code.
	snapshot.values[24] = 999
	if got := c.grblSettingsSnapshotCopy().values[24]; got != 30 {
		t.Fatalf("mutating copied snapshot changed controller setting $24 to %v", got)
	}
}

func TestConnectSettingsSnapshotPreservesPermissiveHandshake(t *testing.T) {
	tests := []struct {
		name       string
		settings   []string
		wantValues map[int]float64
	}{
		{
			name:       "malformed numeric setting",
			settings:   []string{"$24=abc", "ok"},
			wantValues: map[int]float64{},
		},
		{
			name:       "partial settings dump",
			settings:   []string{"$25=2000.000 (homeSeek)", "ok"},
			wantValues: map[int]float64{25: 2000},
		},
		{
			name:       "mixed valid and malformed settings",
			settings:   []string{"$24=30.000", "$25=not-a-number", "$130=86.500 (x:mm)", "ok"},
			wantValues: map[int]float64{24: 30, 130: 86.5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newBlockingOpenTransport()
			c := newManualHandshakeController(tr)
			done := startManualHandshake(t, tr, c)
			completeManualHandshake(t, tr, 1, tt.settings...)
			if err := <-done; err != nil {
				t.Fatalf("Connect() error = %v", err)
			}
			snapshot := c.grblSettingsSnapshotCopy()
			if snapshot.generation != 1 || len(snapshot.values) != len(tt.wantValues) {
				t.Fatalf("settings snapshot = %+v, want generation 1 values %#v", snapshot, tt.wantValues)
			}
			for number, want := range tt.wantValues {
				if got := snapshot.values[number]; got != want {
					t.Errorf("setting $%d = %v, want %v", number, got, want)
				}
			}
		})
	}
}

func TestFailedConnectDoesNotPublishCandidateGRBLSettings(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)
	done := startManualHandshake(t, tr, c)
	completeManualHandshake(t, tr, 1, "$24=30.000", "$25=2000.000", "error:2")
	if err := <-done; err == nil || !strings.Contains(err.Error(), "error:2") {
		t.Fatalf("Connect() error = %v, want settings-query failure", err)
	}
	if snapshot := c.grblSettingsSnapshotCopy(); snapshot.generation != 0 || snapshot.values != nil {
		t.Fatalf("failed candidate published settings: %+v", snapshot)
	}
}

func TestGRBLSettingsSnapshotClearsAndReplacesAcrossGenerations(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)

	first := startManualHandshake(t, tr, c)
	completeManualHandshake(t, tr, 1, "$24=30.000", "$130=86.500", "ok")
	if err := <-first; err != nil {
		t.Fatalf("generation A Connect() error = %v", err)
	}
	if snapshot := c.grblSettingsSnapshotCopy(); snapshot.generation != 1 || snapshot.values[24] != 30 {
		t.Fatalf("generation A snapshot = %+v", snapshot)
	}
	if err := c.Disconnect(); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	if snapshot := c.grblSettingsSnapshotCopy(); snapshot.generation != 0 || snapshot.values != nil {
		t.Fatalf("disconnect left settings snapshot: %+v", snapshot)
	}

	second := make(chan error, 1)
	go func() {
		second <- c.Connect(context.Background(), transport.DefaultPortConfig("generation-B"))
	}()
	waitForAttemptGeneration(t, c, 2)
	injectHandshakeRX(tr, 2, "Grbl 1.1g [help:'$']")
	waitForDisplayWriteCount(t, tr, "$$", 2)
	injectHandshakeRX(tr, 2, "$25=1500.000", "$131=200.000", "ok")
	if err := <-second; err != nil {
		t.Fatalf("generation B Connect() error = %v", err)
	}
	snapshotB := c.grblSettingsSnapshotCopy()
	if snapshotB.generation != 2 || snapshotB.values[25] != 1500 || snapshotB.values[131] != 200 {
		t.Fatalf("generation B snapshot = %+v", snapshotB)
	}
	if _, retained := snapshotB.values[24]; retained {
		t.Fatalf("generation B retained generation A setting $24: %+v", snapshotB)
	}

	// Stale post-commit traffic is not a settings-snapshot mutation path.
	injectHandshakeRX(tr, 1, "$24=999.000")
	injectHandshakeRX(tr, 2, "<Idle|MPos:0,0,0>")
	waitForState(t, c, func(state State) bool { return state.MachineState == "Idle" })
	afterStale := c.grblSettingsSnapshotCopy()
	if afterStale.generation != snapshotB.generation || len(afterStale.values) != len(snapshotB.values) || afterStale.values[25] != 1500 {
		t.Fatalf("stale generation changed settings snapshot: before=%+v after=%+v", snapshotB, afterStale)
	}
}

func TestSetGRBLSettingsLockedCopiesCandidateValues(t *testing.T) {
	c := NewController(transport.NewFakeTransport(), nil)
	candidate := map[int]float64{24: 30}
	c.mu.Lock()
	c.setGRBLSettingsLocked(7, candidate)
	c.mu.Unlock()
	candidate[24] = 999
	if snapshot := c.grblSettingsSnapshotCopy(); snapshot.generation != 7 || snapshot.values[24] != 30 {
		t.Fatalf("candidate map aliased committed snapshot: %+v", snapshot)
	}
}

func TestProductionExpectedSilenceRequiresCompleteSettingsSnapshot(t *testing.T) {
	fake := transport.NewFakeTransport()
	c := NewController(fake, nil)
	c.statusPollInterval = time.Hour
	if err := c.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = fake.Close() })
	fake.SetResponding(false)
	if err := c.SendConsoleLine(context.Background(), "$H"); err != nil {
		t.Fatalf("SendConsoleLine($H) error = %v", err)
	}
	if lease := expectedSilenceSnapshot(c); lease.id != 0 {
		t.Fatalf("incomplete handshake settings unexpectedly started silence lease: %+v", lease)
	}
	fake.InjectRX("ok")
}
