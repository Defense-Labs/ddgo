package grbl

import "testing"

func TestParseStartupBanner(t *testing.T) {
	tests := []struct {
		line        string
		wantVersion string
		wantOK      bool
	}{
		{"Grbl 1.1g [help:'$']", "1.1", true},
		{"grbl 0.9j ['$' for help]", "0.9", true},
		{"GRBL 12.34", "12.34", true},
		{"prefix Grbl 1.1g", "", false},
		{" Grbl 1.1g", "", false},
		{"Grbl v1.1", "", false},
		{"Grbl 1", "", false},
		{"Grbl ", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			version, ok := ParseStartupBanner(tt.line)
			if version != tt.wantVersion || ok != tt.wantOK {
				t.Fatalf("ParseStartupBanner(%q) = (%q, %t), want (%q, %t)", tt.line, version, ok, tt.wantVersion, tt.wantOK)
			}
		})
	}
}

func TestIsSettingLine(t *testing.T) {
	for _, line := range []string{"$0=10", "$1=25", "$100=40.000", "$13=0"} {
		if !IsSettingLine(line) {
			t.Errorf("IsSettingLine(%q) = false, want true", line)
		}
	}
	for _, line := range []string{"", "ok", "$=10", "$A=10", "$0=", "prefix $0=10", " $0=10"} {
		if IsSettingLine(line) {
			t.Errorf("IsSettingLine(%q) = true, want false", line)
		}
	}
}
