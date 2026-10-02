package frontend

import "testing"

func TestCommandHistoryEmpty(t *testing.T) {
	var h CommandHistory
	if got := h.Previous(""); got != "" {
		t.Fatalf("Previous() = %q, want empty", got)
	}
	if got := h.Next(); got != "" {
		t.Fatalf("Next() = %q, want empty", got)
	}
}

func TestCommandHistoryTraversalAndBoundaries(t *testing.T) {
	var h CommandHistory
	for _, command := range []string{"A", "B", "C"} {
		h.Add(command)
	}

	if got := h.Previous(""); got != "C" {
		t.Fatalf("first Previous call = %q, want C", got)
	}
	for i, want := range []string{"B", "A", "A"} {
		if got := h.Previous("ignored while browsing"); got != want {
			t.Fatalf("Previous call %d while browsing = %q, want %q", i+2, got, want)
		}
	}
	for i, want := range []string{"B", "C", "", ""} {
		if got := h.Next(); got != want {
			t.Fatalf("Next call %d = %q, want %q", i, got, want)
		}
	}
}

func TestCommandHistoryPreservesDraft(t *testing.T) {
	var h CommandHistory
	h.Add("A")
	h.Add("B")

	if got := h.Previous("XYZ"); got != "B" {
		t.Fatalf("first Previous() = %q, want B", got)
	}
	if got := h.Previous("edited recalled command"); got != "A" {
		t.Fatalf("second Previous() = %q, want A", got)
	}
	if got := h.Next(); got != "B" {
		t.Fatalf("first Next() = %q, want B", got)
	}
	if got := h.Next(); got != "XYZ" {
		t.Fatalf("draft restoration = %q, want XYZ", got)
	}
}

func TestCommandHistoryBlankDraft(t *testing.T) {
	var h CommandHistory
	h.Add("A")
	if got := h.Previous(""); got != "A" {
		t.Fatalf("Previous() = %q, want A", got)
	}
	if got := h.Next(); got != "" {
		t.Fatalf("Next() = %q, want empty draft", got)
	}
}

func TestCommandHistoryRetainsDuplicates(t *testing.T) {
	var h CommandHistory
	h.Add("A")
	h.Add("A")

	if got := h.Previous(""); got != "A" || h.index != 1 {
		t.Fatalf("first Previous() = %q at index %d, want A at index 1", got, h.index)
	}
	if got := h.Previous("A"); got != "A" || h.index != 0 {
		t.Fatalf("second Previous() = %q at index %d, want A at index 0", got, h.index)
	}
}

func TestCommandHistoryEditingRecallDoesNotMutateEntry(t *testing.T) {
	var h CommandHistory
	h.Add("A")
	if got := h.Previous(""); got != "A" {
		t.Fatalf("Previous() = %q, want A", got)
	}

	// Text edited in the QLineEdit is not written through to the history.
	if got := h.Previous("edited A"); got != "A" {
		t.Fatalf("Previous() after edit = %q, want original A", got)
	}
}

func TestCommandHistoryAddResetsNavigation(t *testing.T) {
	var h CommandHistory
	h.Add("A")
	h.Add("B")
	h.Previous("draft")
	h.Previous("B")

	h.Add("C")
	if h.index != len(h.entries) || h.draft != "" {
		t.Fatalf("Add did not reset navigation: index=%d entries=%d draft=%q", h.index, len(h.entries), h.draft)
	}
	if got := h.Previous(""); got != "C" {
		t.Fatalf("Previous() after Add = %q, want C", got)
	}
}
