package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ianbruene/ddgo/internal/transport"
)

func connectControllerLineTest(t *testing.T) (*Controller, *transport.FakeTransport) {
	t.Helper()
	fake := transport.NewFakeTransport()
	c := NewController(fake, nil)
	c.statusPollInterval = time.Hour
	if err := c.Connect(context.Background(), transport.DefaultPortConfig("fake")); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = fake.Close() })
	c.expectedSilencePolicy = func(string) (expectedSilenceClass, time.Duration, bool) {
		return expectedSilenceHomeAll, time.Hour, true
	}
	return c, fake
}

func assertInvalidControllerLineLeftNoCommandState(t *testing.T, c *Controller, fake *transport.FakeTransport, writesBefore int, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvalidControllerLine) {
		t.Fatalf("error = %v, want ErrInvalidControllerLine", err)
	}
	if got := len(fake.Written()); got != writesBefore {
		t.Fatalf("transport writes = %d, want unchanged %d", got, writesBefore)
	}
	c.mu.RLock()
	owner := c.responseOwner
	lease := c.expectedSilence
	c.mu.RUnlock()
	if owner.kind != responseOwnerNone {
		t.Fatalf("invalid line left response owner: %+v", owner)
	}
	if lease.id != 0 {
		t.Fatalf("invalid line started expected-silence lease: %+v", lease)
	}
}

func TestConsoleRejectsMultiLineControllerCommands(t *testing.T) {
	c, fake := connectControllerLineTest(t)
	for _, line := range []string{"$H\n$X", "$H\r$X", "G0 X1\nG0 X2"} {
		t.Run(line, func(t *testing.T) {
			writesBefore := len(fake.Written())
			err := c.SendConsoleLine(context.Background(), line)
			assertInvalidControllerLineLeftNoCommandState(t, c, fake, writesBefore, err)
		})
	}
}

func TestWriteManualLineRejectsMultiLineControllerCommand(t *testing.T) {
	c, fake := connectControllerLineTest(t)
	writesBefore := len(fake.Written())
	err := c.writeManualLine(context.Background(), "G0 X1\r\nG0 X2")
	assertInvalidControllerLineLeftNoCommandState(t, c, fake, writesBefore, err)
}

func TestManualResponseMessageRejectsMultiLineControllerCommand(t *testing.T) {
	c, fake := connectControllerLineTest(t)
	writesBefore := len(fake.Written())
	err := c.writeManualResponseMessage(context.Background(), transport.NewLineMessage("$H\n$X"))
	assertInvalidControllerLineLeftNoCommandState(t, c, fake, writesBefore, err)
}

func TestCommandRuntimeRejectsMultiLineControllerCommands(t *testing.T) {
	c, fake := connectControllerLineTest(t)
	session, err := c.beginInteractiveSession(context.Background())
	if err != nil {
		t.Fatalf("beginInteractiveSession() error = %v", err)
	}
	t.Cleanup(func() { c.endInteractiveSession(session, nil) })
	runtime := c.runtimeForSession(session)
	writesBefore := len(fake.Written())

	if err := runtime.sendLineAndWaitOK(context.Background(), "$H\n$X", 0); !errors.Is(err, ErrInvalidControllerLine) {
		t.Fatalf("sendLineAndWaitOK() error = %v, want ErrInvalidControllerLine", err)
	}
	if _, err := runtime.sendLineCollectingResponses(context.Background(), "$#\r$$"); !errors.Is(err, ErrInvalidControllerLine) {
		t.Fatalf("sendLineCollectingResponses() error = %v, want ErrInvalidControllerLine", err)
	}
	if got := len(fake.Written()); got != writesBefore {
		t.Fatalf("transport writes = %d, want unchanged %d", got, writesBefore)
	}

	c.mu.RLock()
	owner := c.responseOwner
	lease := c.expectedSilence
	queryCh := session.queryRxCh
	c.mu.RUnlock()
	if owner.kind != responseOwnerInteractiveMacro || owner.session != session {
		t.Fatalf("invalid runtime line changed persistent owner: %+v", owner)
	}
	if lease.id != 0 {
		t.Fatalf("invalid runtime line started expected-silence lease: %+v", lease)
	}
	if queryCh != nil {
		t.Fatal("invalid response-collecting line installed query channel")
	}
}
