// Package frontend contains toolkit-independent frontend behavior.
package frontend

// CommandHistory keeps the commands submitted from the manual command entry
// and the transient navigation state for that entry.
type CommandHistory struct {
	entries []string
	index   int
	draft   string
}

func (h *CommandHistory) CanPrevious() bool {
	return len(h.entries) > 0
}

func (h *CommandHistory) Browsing() bool {
	return h.index < len(h.entries)
}

// Add records a submitted command and returns navigation to the live input.
func (h *CommandHistory) Add(command string) {
	h.entries = append(h.entries, command)
	h.index = len(h.entries)
	h.draft = ""
}

// Previous moves to an older command. When leaving the live input, current is
// saved so it can be restored after navigating forward through the history.
func (h *CommandHistory) Previous(current string) string {
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

// Next moves to a newer command, restoring the saved draft at the live input.
func (h *CommandHistory) Next() string {
	if h.index >= len(h.entries) {
		return h.draft
	}
	h.index++
	if h.index == len(h.entries) {
		return h.draft
	}
	return h.entries[h.index]
}
