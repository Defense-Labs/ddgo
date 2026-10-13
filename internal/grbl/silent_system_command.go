package grbl

// SilentSystemCommand identifies GG3 system commands whose firmware routines
// can defer normal realtime status processing for an extended period.
type SilentSystemCommand uint8

const (
	SilentSystemNone SilentSystemCommand = iota
	SilentSystemHomeAll
	SilentSystemHomeX
	SilentSystemHomeY
	SilentSystemHomeZ
	SilentSystemLevel
	SilentSystemLevelSave
)

// CanonicalizeSystemLine applies the GG3 firmware's relevant single-line input
// filtering for command classification. It is not a general-purpose G-code
// parser and does not model serial framing or firmware buffer limits.
func CanonicalizeSystemLine(line string) string {
	canonical := make([]byte, 0, len(line))
	inParenthesesComment := false
	for i := 0; i < len(line); i++ {
		char := line[i]
		if inParenthesesComment {
			if char == ')' {
				inParenthesesComment = false
			}
			continue
		}
		switch {
		case char == ';':
			return string(canonical)
		case char == '(':
			inParenthesesComment = true
		case char == '/', char <= ' ':
			continue
		case char >= 'a' && char <= 'z':
			canonical = append(canonical, char-'a'+'A')
		default:
			canonical = append(canonical, char)
		}
	}
	return string(canonical)
}

// ClassifySilentSystemCommand recognizes exactly the GG3 system commands known
// to have long normal-status blind periods. Timing policy is intentionally not
// part of this classification.
func ClassifySilentSystemCommand(line string) SilentSystemCommand {
	switch CanonicalizeSystemLine(line) {
	case "$H":
		return SilentSystemHomeAll
	case "$HX":
		return SilentSystemHomeX
	case "$HY":
		return SilentSystemHomeY
	case "$HZ":
		return SilentSystemHomeZ
	case "$L":
		return SilentSystemLevel
	case "$LS":
		return SilentSystemLevelSave
	default:
		return SilentSystemNone
	}
}
