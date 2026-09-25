package grbl

import (
	"strconv"
	"strings"
)

// ParseAlarm parses a complete GRBL ALARM:<number> line.
func ParseAlarm(line string) (int, bool) {
	line = strings.TrimSpace(line)
	prefix, codeText, ok := strings.Cut(line, ":")
	if !ok || !strings.EqualFold(strings.TrimSpace(prefix), "ALARM") {
		return 0, false
	}
	codeText = strings.TrimSpace(codeText)
	if codeText == "" {
		return 0, false
	}
	for _, r := range codeText {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	code, err := strconv.Atoi(codeText)
	return code, err == nil
}
