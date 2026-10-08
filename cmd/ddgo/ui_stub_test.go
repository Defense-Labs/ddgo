//go:build !miqt

package main

import (
	"errors"
	"testing"
)

func TestRunUIWithoutMIQT(t *testing.T) {
	t.Parallel()

	err := runUI(nil)
	if !errors.Is(err, errMIQTNotBuilt) {
		t.Fatalf("runUI() error = %v, want %v", err, errMIQTNotBuilt)
	}
	if got, want := err.Error(), "MIQT UI not built; rebuild with -tags miqt"; got != want {
		t.Fatalf("runUI() error text = %q, want %q", got, want)
	}
}
