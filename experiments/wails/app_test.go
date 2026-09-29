package main

import "testing"

func TestSyntheticToolpathShape(t *testing.T) {
	path := NewApp().GetToolpath()
	if len(path) < 1000 || len(path) > 5000 {
		t.Fatalf("segment count = %d, want capability-spike range 1,000-5,000", len(path))
	}
	if !path[len(path)-1].Rapid || path[len(path)-1].End.Z != 12 {
		t.Fatalf("final segment = %+v, want final rapid retract to Z 12", path[len(path)-1])
	}

	var cutting, rapid, verticalCuts int
	for _, segment := range path {
		if segment.Rapid {
			rapid++
		} else {
			cutting++
			if segment.Start.X == segment.End.X && segment.Start.Y == segment.End.Y && segment.Start.Z != segment.End.Z {
				verticalCuts++
			}
		}
	}
	if cutting == 0 || rapid == 0 || verticalCuts == 0 {
		t.Fatalf("cutting=%d rapid=%d vertical cutting moves=%d, want all motion types", cutting, rapid, verticalCuts)
	}
}
