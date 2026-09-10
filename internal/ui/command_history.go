package ui

// commandHistory keeps the commands submitted from the manual command entry
// and the transient navigation state for that entry.
type commandHistory struct {
	entries []string
	index   int
	draft   string
}

func (h *commandHistory) canPrevious() bool {
	return len(h.entries) > 0
}

func (h *commandHistory) browsing() bool {
	return h.index < len(h.entries)
}

// add records a submitted command and returns navigation to the live input.
func (h *commandHistory) add(command string) {
	h.entries = append(h.entries, command)
	h.index = len(h.entries)
	h.draft = ""
}

// previous moves to an older command. When leaving the live input, current is
// saved so it can be restored after navigating forward through the history.
func (h *commandHistory) previous(current string) string {
	if len(h.entries) == 0 {
		return current
	}
	if h.index >= len(h.entries) {
		h.index = len(h.entries)
		h.draft = current
	}
	if h.index > 0 {
		h.index--
	}
	return h.entries[h.index]
}

// next moves to a newer command, restoring the saved draft at the live input.
func (h *commandHistory) next() string {
	if h.index >= len(h.entries) {
		return h.draft
	}
	h.index++
	if h.index == len(h.entries) {
		return h.draft
	}
	return h.entries[h.index]
}
