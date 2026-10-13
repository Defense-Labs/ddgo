package grbl

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var startupBannerPattern = regexp.MustCompile(`(?i)^Grbl ([0-9]+\.[0-9]+).*`)
var settingLinePattern = regexp.MustCompile(`^\$[0-9]+=.+$`)

// ParseStartupBanner recognizes the startup line used by GRBL-compatible
// controllers and returns its numeric protocol version.
func ParseStartupBanner(line string) (string, bool) {
	match := startupBannerPattern.FindStringSubmatch(line)
	if match == nil {
		return "", false
	}
	return match[1], true
}

// IsSettingLine reports whether line has the shape of one entry from a GRBL
// settings dump.
func IsSettingLine(line string) bool {
	return settingLinePattern.MatchString(line)
}

// ParseSettingLine parses the numeric portion of one GRBL settings-dump line.
// Firmware-provided descriptive text following the whitespace-delimited numeric
// value is ignored.
func ParseSettingLine(line string) (number int, value float64, ok bool) {
	if len(line) < 4 || line[0] != '$' {
		return 0, 0, false
	}
	equals := strings.IndexByte(line, '=')
	if equals < 2 {
		return 0, 0, false
	}
	for _, digit := range line[1:equals] {
		if digit < '0' || digit > '9' {
			return 0, 0, false
		}
	}
	parsedNumber, err := strconv.Atoi(line[1:equals])
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(line[equals+1:])
	if len(fields) == 0 {
		return 0, 0, false
	}
	parsedValue, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(parsedValue) || math.IsInf(parsedValue, 0) {
		return 0, 0, false
	}
	return parsedNumber, parsedValue, true
}
