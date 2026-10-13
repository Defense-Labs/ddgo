package grbl

import "testing"

func TestParseGG3LevelAdjustment(t *testing.T) {
	for _, tt := range []struct {
		line string
		want float64
	}{
		{"[0.123 mm]", 0.123},
		{"[-0.456 mm]", -0.456},
		{"[0.000 mm]", 0},
		{"[+1.5 mm]", 1.5},
		{" [.25 mm] ", 0.25},
	} {
		t.Run(tt.line, func(t *testing.T) {
			got, ok := ParseGG3LevelAdjustment(tt.line)
			if !ok || got != tt.want {
				t.Fatalf("ParseGG3LevelAdjustment(%q) = (%v, %t), want (%v, true)", tt.line, got, ok, tt.want)
			}
		})
	}
}

func TestParseGG3LevelAdjustmentRejectsMalformed(t *testing.T) {
	for _, line := range []string{
		"", "[NaN mm]", "[Inf mm]", "[-Inf mm]", "[1.0 in]", "[1.0mm]",
		"[1.0 mm", "1.0 mm]", "[MSG:1.0 mm]", "[1.0 mm] trailing", "ok",
	} {
		t.Run(line, func(t *testing.T) {
			if value, ok := ParseGG3LevelAdjustment(line); ok {
				t.Fatalf("ParseGG3LevelAdjustment(%q) = (%v, true), want ok=false", line, value)
			}
		})
	}
}

func TestIsGG3LevelAdjustmentLike(t *testing.T) {
	for _, line := range []string{"[NaN mm]", "[Inf mm]", "[1..0 mm]", "[1.0 mm]"} {
		if !IsGG3LevelAdjustmentLike(line) {
			t.Errorf("IsGG3LevelAdjustmentLike(%q) = false, want true", line)
		}
	}
	for _, line := range []string{"[1.0 in]", "[MSG:hello]", "[MSG:1.0 mm]", "[bad mm]", "1.0 mm]", "[1.0 mm] trailing"} {
		if IsGG3LevelAdjustmentLike(line) {
			t.Errorf("IsGG3LevelAdjustmentLike(%q) = true, want false", line)
		}
	}
}
