package grbl

import "testing"

func TestCanonicalizeSystemLine(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"$H", "$H"},
		{"$h", "$H"},
		{" $ h ", "$H"},
		{"$H (home)", "$H"},
		{"$H(foo)X", "$HX"},
		{"$ h ; comment", "$H"},
		{"/$h", "$H"},
		{"", ""},
		{"(comment only)", ""},
		{"; comment only", ""},
		{"$H(unterminated comment", "$H"},
		{"$H;comment)X", "$H"},
		{"\t$\rh\n", "$H"},
		{"$l(foo; still parentheses) s", "$LS"},
		{"$h(foo)/x", "$HX"},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			if got := CanonicalizeSystemLine(tt.line); got != tt.want {
				t.Fatalf("CanonicalizeSystemLine(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestClassifySilentSystemCommand(t *testing.T) {
	tests := []struct {
		line string
		want SilentSystemCommand
	}{
		{"$H", SilentSystemHomeAll},
		{" $ h (home all)", SilentSystemHomeAll},
		{"$HX", SilentSystemHomeX},
		{"$h (comment) x", SilentSystemHomeX},
		{"/$hy", SilentSystemHomeY},
		{"$H Z ; home Z", SilentSystemHomeZ},
		{"$L", SilentSystemLevel},
		{" $ l ; level", SilentSystemLevel},
		{"$LS", SilentSystemLevelSave},
		{"$l(save)s", SilentSystemLevelSave},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			if got := ClassifySilentSystemCommand(tt.line); got != tt.want {
				t.Fatalf("ClassifySilentSystemCommand(%q) = %d, want %d", tt.line, got, tt.want)
			}
		})
	}
}

func TestClassifySilentSystemCommandRejectsNearMisses(t *testing.T) {
	for _, line := range []string{
		"",
		"$HOME",
		"$HXYZ",
		"$LFOO",
		"$LS1",
		"$L X",
		"$X",
		"$$",
		"$#",
		"$J=G91X1F100",
		"G28",
		"G38.2 X-1",
	} {
		t.Run(line, func(t *testing.T) {
			if got := ClassifySilentSystemCommand(line); got != SilentSystemNone {
				t.Fatalf("ClassifySilentSystemCommand(%q) = %d, want SilentSystemNone", line, got)
			}
		})
	}
}
