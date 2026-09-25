package grbl

import "testing"

func TestParseAlarm(t *testing.T) {
	t.Parallel()
	tests := []struct {
		line string
		code int
		ok   bool
	}{
		{line: "ALARM:50", code: 50, ok: true},
		{line: " alarm:50 ", code: 50, ok: true},
		{line: "ALARM:1", code: 1, ok: true},
		{line: "ALARM:", ok: false},
		{line: "ALARM:x", ok: false},
		{line: "error:50", ok: false},
		{line: "random text", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			code, ok := ParseAlarm(tt.line)
			if code != tt.code || ok != tt.ok {
				t.Fatalf("ParseAlarm(%q) = (%d, %v), want (%d, %v)", tt.line, code, ok, tt.code, tt.ok)
			}
		})
	}
}
