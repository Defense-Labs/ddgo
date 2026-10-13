package grbl

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var gg3LevelAdjustmentPattern = regexp.MustCompile(`^\[([+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)) mm\]$`)

// ParseGG3LevelAdjustment parses the adjustment progress line emitted by the
// examined GG3 firmware's $L command.
func ParseGG3LevelAdjustment(line string) (float64, bool) {
	match := gg3LevelAdjustmentPattern.FindStringSubmatch(strings.TrimSpace(line))
	if match == nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

// IsGG3LevelAdjustmentLike reports whether a line has the firmware adjustment
// envelope even when its numeric payload cannot be trusted.
func IsGG3LevelAdjustmentLike(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, " mm]") {
		return false
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(line, "["), " mm]")
	if payload == "" {
		return false
	}
	first := payload[0]
	return first == '+' || first == '-' || first == '.' ||
		(first >= '0' && first <= '9') || first == 'N' || first == 'I'
}
