package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ianbruene/ddgo/internal/transport"
)

func newManualHandshakeController(tr *blockingOpenTransport) *Controller {
	tr.autoHandshake = false
	c := NewController(tr, nil)
	c.connectionBannerTimeout = 500 * time.Millisecond
	c.connectionStartupQuietPeriod = 20 * time.Millisecond
	c.connectionStartupSettleTimeout = 150 * time.Millisecond
	c.connectionSettingsTimeout = 500 * time.Millisecond
	c.statusPollInterval = time.Hour
	return c
}

func startManualHandshake(t *testing.T, tr *blockingOpenTransport, c *Controller) <-chan error {
	t.Helper()
	done := beginBlockedConnect(t, tr, c, context.Background())
	close(tr.releaseOpen)
	waitForAttemptGeneration(t, c, 1)
	return done
}

func waitForAttemptGeneration(t *testing.T, c *Controller, want transport.ConnectionGeneration) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		got := c.connectAttemptGeneration
		c.mu.RUnlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("connect attempt generation did not become %d", want)
}

func waitForDisplayWriteCount(t *testing.T, tr *blockingOpenTransport, display string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tr.mu.Lock()
		count := 0
		for _, write := range tr.writes {
			if write.Display == display {
				count++
			}
		}
		tr.mu.Unlock()
		if count >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d %q writes", want, display)
}

func displayWriteCount(tr *blockingOpenTransport, display string) int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	count := 0
	for _, write := range tr.writes {
		if write.Display == display {
			count++
		}
	}
	return count
}

func injectHandshakeRX(tr *blockingOpenTransport, generation transport.ConnectionGeneration, lines ...string) {
	for _, line := range lines {
		tr.events <- transport.Event{Kind: transport.EventRX, Generation: generation, When: time.Now(), Text: line}
	}
}

func completeManualHandshake(t *testing.T, tr *blockingOpenTransport, generation transport.ConnectionGeneration, settings ...string) {
	t.Helper()
	injectHandshakeRX(tr, generation, "Grbl 1.1g [help:'$']")
	waitForDisplayWriteCount(t, tr, "$$", 1)
	if len(settings) == 0 {
		settings = []string{"$0=10", "$1=25", "ok"}
	}
	injectHandshakeRX(tr, generation, settings...)
}

func TestConnectWaitsForGRBLHandshakeBeforeCommit(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)
	done := startManualHandshake(t, tr, c)

	if got := c.Snapshot().ConnectionStatus; got != ConnectionConnecting {
		t.Fatalf("status after Open = %q, want %q", got, ConnectionConnecting)
	}
	c.mu.RLock()
	committedGeneration := c.connectionGeneration
	c.mu.RUnlock()
	if committedGeneration != 0 || c.Snapshot().PortName != "" {
		t.Fatalf("candidate committed before validation: generation=%d state=%+v", committedGeneration, c.Snapshot())
	}
	if got := displayWriteCount(tr, "$$"); got != 0 {
		t.Fatalf("settings writes before banner = %d, want 0", got)
	}
	select {
	case err := <-done:
		t.Fatalf("Open alone completed Connect with %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	injectHandshakeRX(tr, 1, "not a banner")
	time.Sleep(30 * time.Millisecond)
	if got := displayWriteCount(tr, "$$"); got != 0 {
		t.Fatalf("settings writes after invalid banner = %d, want 0", got)
	}
	injectHandshakeRX(tr, 1, "Grbl 1.1g [help:'$']")
	time.Sleep(10 * time.Millisecond)
	injectHandshakeRX(tr, 1, "[MSG:startup]")
	time.Sleep(10 * time.Millisecond)
	if got := displayWriteCount(tr, "$$"); got != 0 {
		t.Fatalf("settings write occurred before startup quiet period")
	}
	waitForDisplayWriteCount(t, tr, "$$", 1)
	if c.Snapshot().ConnectionStatus != ConnectionConnecting {
		t.Fatalf("status during settings query = %q, want connecting", c.Snapshot().ConnectionStatus)
	}
	injectHandshakeRX(tr, 1, "$0=10", "$1=25", "ok")
	if err := <-done; err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if got := c.Snapshot().ConnectionStatus; got != ConnectionConnected {
		t.Fatalf("final status = %q, want %q", got, ConnectionConnected)
	}

	events := collectEventsThroughText(t, c.Events(), "connected to blocked")
	var connecting, connected Event
	for _, event := range events {
		if event.Kind == EventConsoleRX || event.Kind == EventConsoleTX {
			t.Fatalf("handshake traffic reached normal console: %+v", event)
		}
		if event.Kind == EventStateChanged && event.Text == "connecting to blocked" {
			connecting = event
		}
		if event.Kind == EventStateChanged && event.Text == "connected to blocked" {
			connected = event
		}
	}
	if connecting.State.ConnectionStatus != ConnectionConnecting || connected.State.ConnectionStatus != ConnectionConnected || connecting.StateRevision >= connected.StateRevision {
		t.Fatalf("connection events out of order: connecting=%+v connected=%+v", connecting, connected)
	}
}

func TestConnectRetainsBannerArrivingBeforeOpenReturns(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)
	done := beginBlockedConnect(t, tr, c, context.Background())
	injectHandshakeRX(tr, 1, "Grbl 1.1g [help:'$']")
	deadline := time.Now().Add(2 * time.Second)
	queuedBeforeOpenReturned := false
	for time.Now().Before(deadline) {
		c.mu.RLock()
		queued := len(c.connectAttemptEvents)
		c.mu.RUnlock()
		if queued > 0 {
			queuedBeforeOpenReturned = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !queuedBeforeOpenReturned {
		t.Fatal("startup banner was not retained while Open was blocked")
	}
	close(tr.releaseOpen)
	waitForAttemptGeneration(t, c, 1)
	waitForDisplayWriteCount(t, tr, "$$", 1)
	injectHandshakeRX(tr, 1, "$0=10", "ok")
	if err := <-done; err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
}

func TestConnectRetainsErrorArrivingBeforeOpenReturns(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)
	done := beginBlockedConnect(t, tr, c, context.Background())
	want := errors.New("early candidate read failed")
	tr.events <- transport.Event{Kind: transport.EventError, Generation: 1, When: time.Now(), Err: want}
	deadline := time.Now().Add(2 * time.Second)
	queuedBeforeOpenReturned := false
	for time.Now().Before(deadline) {
		c.mu.RLock()
		queued := len(c.connectAttemptEvents)
		c.mu.RUnlock()
		if queued > 0 {
			queuedBeforeOpenReturned = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !queuedBeforeOpenReturned {
		t.Fatal("transport error was not retained while Open was blocked")
	}
	close(tr.releaseOpen)
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("Connect() error = %v, want %v", err, want)
	}
}

func TestConnectBannerDeadlineIgnoresJunkActivity(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)
	c.connectionBannerTimeout = 70 * time.Millisecond
	done := startManualHandshake(t, tr, c)
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				injectHandshakeRX(tr, 1, "junk")
			}
		}
	}()
	err := <-done
	close(stop)
	if err == nil || !strings.Contains(err.Error(), "startup banner") {
		t.Fatalf("Connect() error = %v, want banner timeout", err)
	}
	if c.Snapshot().ConnectionStatus != ConnectionDisconnected {
		t.Fatalf("status after banner timeout = %q", c.Snapshot().ConnectionStatus)
	}
	if _, closes, _ := tr.counts(); closes != 1 {
		t.Fatalf("Close calls = %d, want 1", closes)
	}
}

func TestConnectStartupQuietPeriodHasAbsoluteBound(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)
	c.connectionStartupSettleTimeout = 70 * time.Millisecond
	done := startManualHandshake(t, tr, c)
	injectHandshakeRX(tr, 1, "Grbl 1.1g [help:'$']")
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				injectHandshakeRX(tr, 1, "[MSG:still starting]")
			}
		}
	}()
	err := <-done
	close(stop)
	if err == nil || !strings.Contains(err.Error(), "did not settle") {
		t.Fatalf("Connect() error = %v, want startup settle timeout", err)
	}
}

func TestConnectIgnoresStaleGenerationHandshakeTraffic(t *testing.T) {
	t.Run("banner", func(t *testing.T) {
		tr := newBlockingOpenTransport()
		c := newManualHandshakeController(tr)
		done := beginBlockedConnect(t, tr, c, context.Background())
		injectHandshakeRX(tr, 99, "Grbl 1.1g [help:'$']")
		close(tr.releaseOpen)
		waitForAttemptGeneration(t, c, 1)
		time.Sleep(30 * time.Millisecond)
		if got := displayWriteCount(tr, "$$"); got != 0 {
			t.Fatalf("stale banner caused %d settings writes", got)
		}
		completeManualHandshake(t, tr, 1)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("settings", func(t *testing.T) {
		tr := newBlockingOpenTransport()
		c := newManualHandshakeController(tr)
		done := startManualHandshake(t, tr, c)
		injectHandshakeRX(tr, 1, "Grbl 1.1g [help:'$']")
		waitForDisplayWriteCount(t, tr, "$$", 1)
		injectHandshakeRX(tr, 99, "$0=10", "ok")
		time.Sleep(30 * time.Millisecond)
		if c.Snapshot().ConnectionStatus != ConnectionConnecting {
			t.Fatalf("stale settings response committed connection")
		}
		injectHandshakeRX(tr, 1, "$0=10", "ok")
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("transport failures", func(t *testing.T) {
		tr := newBlockingOpenTransport()
		c := newManualHandshakeController(tr)
		done := startManualHandshake(t, tr, c)
		tr.events <- transport.Event{Kind: transport.EventError, Generation: 99, When: time.Now(), Err: errors.New("stale read failure")}
		tr.events <- transport.Event{Kind: transport.EventDisconnected, Generation: 99, When: time.Now()}
		completeManualHandshake(t, tr, 1)
		if err := <-done; err != nil {
			t.Fatalf("stale transport events failed current attempt: %v", err)
		}
	})
}

func TestConnectRejectsInvalidSettingsResponses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"bare ok", []string{"ok"}, "without any settings"},
		{"error", []string{"$0=10", "error:2"}, "error:2"},
		{"alarm", []string{"$0=10", "ALARM:1"}, "ALARM:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newBlockingOpenTransport()
			c := newManualHandshakeController(tr)
			done := startManualHandshake(t, tr, c)
			completeManualHandshake(t, tr, 1, tc.lines...)
			err := <-done
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Connect() error = %v, want substring %q", err, tc.want)
			}
			if c.Snapshot().ConnectionStatus != ConnectionDisconnected {
				t.Fatalf("status = %q, want disconnected", c.Snapshot().ConnectionStatus)
			}
			if _, closes, _ := tr.counts(); closes != 1 {
				t.Fatalf("Close calls = %d, want 1", closes)
			}
		})
	}
}

func TestConnectHandshakeSettingsWriteFailure(t *testing.T) {
	tr := newBlockingOpenTransport()
	want := errors.New("settings write failed")
	tr.writeErr = want
	c := newManualHandshakeController(tr)
	done := startManualHandshake(t, tr, c)
	injectHandshakeRX(tr, 1, "Grbl 1.1g [help:'$']")
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("Connect() error = %v, want %v", err, want)
	}
	if c.Snapshot().ConnectionStatus != ConnectionDisconnected {
		t.Fatalf("status = %q, want disconnected", c.Snapshot().ConnectionStatus)
	}
}

func TestConnectSettingsResponseTimeout(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)
	c.connectionSettingsTimeout = 70 * time.Millisecond
	done := startManualHandshake(t, tr, c)
	injectHandshakeRX(tr, 1, "Grbl 1.1g [help:'$']")
	waitForDisplayWriteCount(t, tr, "$$", 1)
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "settings response") {
		t.Fatalf("Connect() error = %v, want settings response timeout", err)
	}
	if c.Snapshot().ConnectionStatus != ConnectionDisconnected {
		t.Fatalf("status = %q, want disconnected", c.Snapshot().ConnectionStatus)
	}
}

func TestConnectCancellationDuringHandshakeClosesCandidate(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)
	ctx, cancel := context.WithCancel(context.Background())
	done := beginBlockedConnect(t, tr, c, ctx)
	close(tr.releaseOpen)
	waitForAttemptGeneration(t, c, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Connect() error = %v, want context cancellation", err)
	}
	if _, closes, _ := tr.counts(); closes != 1 {
		t.Fatalf("Close calls = %d, want 1", closes)
	}
	if c.Snapshot().ConnectionStatus != ConnectionDisconnected {
		t.Fatalf("status = %q, want disconnected", c.Snapshot().ConnectionStatus)
	}
}

func TestConnectHandshakeFailsPromptlyOnTransportEvents(t *testing.T) {
	transportErr := errors.New("candidate read failed")
	for _, tc := range []struct {
		name     string
		afterDDS bool
		event    transport.Event
		want     error
	}{
		{"disconnect during banner", false, transport.Event{Kind: transport.EventDisconnected}, ErrTransportDisconnected},
		{"disconnect during settings", true, transport.Event{Kind: transport.EventDisconnected}, ErrTransportDisconnected},
		{"error during banner", false, transport.Event{Kind: transport.EventError, Err: transportErr}, transportErr},
		{"error during settings", true, transport.Event{Kind: transport.EventError, Err: transportErr}, transportErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newBlockingOpenTransport()
			c := newManualHandshakeController(tr)
			c.connectionBannerTimeout = 2 * time.Second
			c.connectionSettingsTimeout = 2 * time.Second
			done := startManualHandshake(t, tr, c)
			if tc.afterDDS {
				injectHandshakeRX(tr, 1, "Grbl 1.1g [help:'$']")
				waitForDisplayWriteCount(t, tr, "$$", 1)
			}
			started := time.Now()
			tc.event.Generation = 1
			tc.event.When = time.Now()
			tr.events <- tc.event
			err := <-done
			if !errors.Is(err, tc.want) {
				t.Fatalf("Connect() error = %v, want %v", err, tc.want)
			}
			if elapsed := time.Since(started); elapsed >= time.Second {
				t.Fatalf("transport failure took %s to end handshake", elapsed)
			}
		})
	}
}

func TestConnectHandshakeFailureClosesAndAllowsRetry(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)
	first := startManualHandshake(t, tr, c)
	completeManualHandshake(t, tr, 1, "ok")
	if err := <-first; err == nil {
		t.Fatal("invalid first handshake succeeded")
	}
	if _, closes, _ := tr.counts(); closes != 1 {
		t.Fatalf("Close calls after failed validation = %d, want 1", closes)
	}

	second := make(chan error, 1)
	go func() { second <- c.Connect(context.Background(), transport.DefaultPortConfig("retry")) }()
	waitForAttemptGeneration(t, c, 2)
	injectHandshakeRX(tr, 2, "Grbl 1.1g [help:'$']")
	waitForDisplayWriteCount(t, tr, "$$", 2)
	injectHandshakeRX(tr, 2, "$0=10", "ok")
	if err := <-second; err != nil {
		t.Fatalf("retry Connect() error = %v", err)
	}
	if c.Snapshot().ConnectionStatus != ConnectionConnected {
		t.Fatalf("retry status = %q, want connected", c.Snapshot().ConnectionStatus)
	}
}

func TestConnectHandshakeCleanupErrorPreservesPrimaryCause(t *testing.T) {
	tr := newBlockingOpenTransport()
	tr.closeErr = errors.New("candidate close failed")
	c := newManualHandshakeController(tr)
	done := startManualHandshake(t, tr, c)
	primary := errors.New("candidate read failed")
	tr.events <- transport.Event{Kind: transport.EventError, Generation: 1, When: time.Now(), Err: primary}
	err := <-done
	if !errors.Is(err, primary) || !errors.Is(err, tr.closeErr) {
		t.Fatalf("Connect() error = %v, want both primary and cleanup causes", err)
	}
	if c.Snapshot().ConnectionStatus != ConnectionDisconnected {
		t.Fatalf("status = %q, want disconnected", c.Snapshot().ConnectionStatus)
	}
}

func TestStatusPollingStartsOnlyAfterHandshakeCommit(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)
	c.statusPollInterval = 5 * time.Millisecond
	done := startManualHandshake(t, tr, c)
	time.Sleep(20 * time.Millisecond)
	if got := displayWriteCount(tr, "?"); got != 0 {
		t.Fatalf("status writes before banner = %d, want 0", got)
	}
	injectHandshakeRX(tr, 1, "Grbl 1.1g [help:'$']")
	waitForDisplayWriteCount(t, tr, "$$", 1)
	time.Sleep(20 * time.Millisecond)
	if got := displayWriteCount(tr, "?"); got != 0 {
		t.Fatalf("status writes before settings validation = %d, want 0", got)
	}
	injectHandshakeRX(tr, 1, "$0=10", "ok")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitForDisplayWriteCount(t, tr, "?", 1)
}

func TestConnectAttemptEventQueueOverflowFailsExplicitly(t *testing.T) {
	tr := newBlockingOpenTransport()
	c := newManualHandshakeController(tr)
	done := beginBlockedConnect(t, tr, c, context.Background())
	for i := 0; i < connectAttemptEventCapacity+1; i++ {
		injectHandshakeRX(tr, 99, "junk")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		err := c.connectAttemptErr
		c.mu.RUnlock()
		if errors.Is(err, ErrConnectAttemptEventOverflow) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(tr.releaseOpen)
	err := <-done
	if !errors.Is(err, ErrConnectAttemptEventOverflow) {
		t.Fatalf("Connect() error = %v, want queue overflow", err)
	}
}
