package grbl

import "testing"

func TestParseSettingAssignment(t *testing.T) {
	for _, tt := range []struct {
		line       string
		wantNumber int
		wantValue  float64
	}{
		{"$24=20", 24, 20},
		{" $ 110 = 2000 ", 110, 2000},
		{"/$130=90.5 (travel)", 130, 90.5},
		{"$25=1e3; seek", 25, 1000},
	} {
		t.Run(tt.line, func(t *testing.T) {
			number, value, ok := ParseSettingAssignment(tt.line)
			if !ok || number != tt.wantNumber || value != tt.wantValue {
				t.Fatalf("ParseSettingAssignment(%q) = (%d, %v, %t), want (%d, %v, true)", tt.line, number, value, ok, tt.wantNumber, tt.wantValue)
			}
		})
	}
}

func TestParseSettingAssignmentRejectsNonAssignments(t *testing.T) {
	for _, line := range []string{"$$", "$24", "$24=abc", "$RST=$", "$H", "$24=NaN", "$24=Inf"} {
		if number, value, ok := ParseSettingAssignment(line); ok {
			t.Errorf("ParseSettingAssignment(%q) = (%d, %v, true), want ok=false", line, number, value)
		}
	}
}

func TestClassifySettingsReset(t *testing.T) {
	tests := []struct {
		line string
		want SettingsReset
	}{
		{"$RST=$", SettingsResetGlobal},
		{" $rst = $ ; reset settings", SettingsResetGlobal},
		{"$RST=*", SettingsResetAll},
		{"$RST=#", SettingsResetCoordinates},
		{"$RST", SettingsResetNone},
		{"$RST=$X", SettingsResetNone},
	}
	for _, tt := range tests {
		if got := ClassifySettingsReset(tt.line); got != tt.want {
			t.Errorf("ClassifySettingsReset(%q) = %d, want %d", tt.line, got, tt.want)
		}
	}
}
