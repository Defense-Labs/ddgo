package app

import "strings"

// validateControllerLine enforces the response-ownership invariant that one
// logical controller operation writes exactly one physical serial line. Callers
// must provide command text without CR/LF terminators.
func validateControllerLine(line string) error {
	if strings.ContainsAny(line, "\r\n") {
		return ErrInvalidControllerLine
	}
	return nil
}
