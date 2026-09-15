package grbl

import "regexp"

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
