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

func TestParseSettingLine(t *testing.T) {
	tests := []struct {
		line       string
		wantNumber int
		wantValue  float64
	}{
		{"$0=10", 0, 10},
		{"$24=30.000", 24, 30},
		{"$24=30.000 (homeFine)", 24, 30},
		{"$25=2000.000 (homeSeek)", 25, 2000},
		{"$26=1 (homeDelay)", 26, 1},
		{"$130=86.500 (x:mm)", 130, 86.5},
		{"$27= 0.500   (homePullOff)", 27, 0.5},
		{"$1=-25.5 description", 1, -25.5},
		{"$2=1e2", 2, 100},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			number, value, ok := ParseSettingLine(tt.line)
			if !ok || number != tt.wantNumber || value != tt.wantValue {
				t.Fatalf("ParseSettingLine(%q) = (%d, %v, %t), want (%d, %v, true)", tt.line, number, value, ok, tt.wantNumber, tt.wantValue)
			}
		})
	}
}

func TestParseSettingLineRejectsMalformedInput(t *testing.T) {
	for _, line := range []string{
		"",
		"ok",
		"$=1",
		"$A=1",
		"$24=",
		"$24=abc",
		"$24=NaN",
		"$24=Inf",
		"$24=-Inf",
		"prefix $24=30",
		" $24=30",
		"$24=30suffix",
		"$999999999999999999999999999=1",
	} {
		t.Run(line, func(t *testing.T) {
			if number, value, ok := ParseSettingLine(line); ok {
				t.Fatalf("ParseSettingLine(%q) = (%d, %v, true), want ok=false", line, number, value)
			}
		})
	}
}
