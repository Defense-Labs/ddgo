package ui

import "testing"

func TestCommandHistoryEmpty(t *testing.T) {
	var h commandHistory
	if got := h.previous(""); got != "" {
		t.Fatalf("previous() = %q, want empty", got)
	}
	if got := h.next(); got != "" {
		t.Fatalf("next() = %q, want empty", got)
	}
}

func TestCommandHistoryTraversalAndBoundaries(t *testing.T) {
	var h commandHistory
	for _, command := range []string{"A", "B", "C"} {
		h.add(command)
	}

	for i, want := range []string{"C", "B", "A", "A"} {
		if got := h.previous("ignored after first Up"); got != want {
			t.Fatalf("previous call %d = %q, want %q", i, got, want)
		}
	}
	for i, want := range []string{"B", "C", "", ""} {
		if got := h.next(); got != want {
			t.Fatalf("next call %d = %q, want %q", i, got, want)
		}
	}
}

func TestCommandHistoryPreservesDraft(t *testing.T) {
	var h commandHistory
	h.add("A")
	h.add("B")

	if got := h.previous("XYZ"); got != "B" {
		t.Fatalf("first previous() = %q, want B", got)
	}
	if got := h.previous("edited recalled command"); got != "A" {
		t.Fatalf("second previous() = %q, want A", got)
	}
	if got := h.next(); got != "B" {
		t.Fatalf("first next() = %q, want B", got)
	}
	if got := h.next(); got != "XYZ" {
		t.Fatalf("draft restoration = %q, want XYZ", got)
	}
}

func TestCommandHistoryBlankDraft(t *testing.T) {
	var h commandHistory
	h.add("A")
	if got := h.previous(""); got != "A" {
		t.Fatalf("previous() = %q, want A", got)
	}
	if got := h.next(); got != "" {
		t.Fatalf("next() = %q, want empty draft", got)
	}
}

func TestCommandHistoryRetainsDuplicates(t *testing.T) {
	var h commandHistory
	h.add("A")
	h.add("A")

	if got := h.previous(""); got != "A" || h.index != 1 {
		t.Fatalf("first previous() = %q at index %d, want A at index 1", got, h.index)
	}
	if got := h.previous("A"); got != "A" || h.index != 0 {
		t.Fatalf("second previous() = %q at index %d, want A at index 0", got, h.index)
	}
}

func TestCommandHistoryEditingRecallDoesNotMutateEntry(t *testing.T) {
	var h commandHistory
	h.add("A")
	if got := h.previous(""); got != "A" {
		t.Fatalf("previous() = %q, want A", got)
	}

	// Text edited in the QLineEdit is not written through to the history.
	if got := h.previous("edited A"); got != "A" {
		t.Fatalf("previous() after edit = %q, want original A", got)
	}
}

func TestCommandHistoryAddResetsNavigation(t *testing.T) {
	var h commandHistory
	h.add("A")
	h.add("B")
	h.previous("draft")
	h.previous("B")

	h.add("C")
	if h.index != len(h.entries) || h.draft != "" {
		t.Fatalf("add did not reset navigation: index=%d entries=%d draft=%q", h.index, len(h.entries), h.draft)
	}
	if got := h.previous(""); got != "C" {
		t.Fatalf("previous() after add = %q, want C", got)
	}
}
