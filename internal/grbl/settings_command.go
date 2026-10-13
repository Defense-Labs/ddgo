package grbl

// SettingsReset identifies GRBL settings-reset commands relevant to the
// connection-owned numeric settings snapshot.
type SettingsReset uint8

const (
	SettingsResetNone SettingsReset = iota
	SettingsResetGlobal
	SettingsResetAll
	SettingsResetCoordinates
)

// ParseSettingAssignment canonicalizes one outbound firmware line and parses
// an exact numeric $<number>=<value> assignment.
func ParseSettingAssignment(line string) (number int, value float64, ok bool) {
	return ParseSettingLine(CanonicalizeSystemLine(line))
}

// ClassifySettingsReset recognizes exact canonical GRBL reset commands.
func ClassifySettingsReset(line string) SettingsReset {
	switch CanonicalizeSystemLine(line) {
	case "$RST=$":
		return SettingsResetGlobal
	case "$RST=*":
		return SettingsResetAll
	case "$RST=#":
		return SettingsResetCoordinates
	default:
		return SettingsResetNone
	}
}
