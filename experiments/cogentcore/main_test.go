package main

import "testing"

func TestSyntheticPathIsBatchedIntoTwoMeshes(t *testing.T) {
	path := makeToolpath()
	if got, want := len(path), 1310; got != want {
		t.Fatalf("segments = %d, want %d", got, want)
	}
	cut, rapid := splitPath(path)
	if got, want := len(cut), 1298; got != want {
		t.Fatalf("cut segments = %d, want %d", got, want)
	}
	if got, want := len(rapid), 12; got != want {
		t.Fatalf("rapid segments = %d, want %d", got, want)
	}
	for _, tc := range []struct {
		name     string
		segments []Segment
	}{
		{"cut", cut},
		{"rapid", rapid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mesh := prismMesh(tc.name, tc.segments, 0.4)
			vertices, indexes, _ := mesh.MeshSize()
			if want := len(tc.segments) * 24; vertices != want {
				t.Fatalf("vertices = %d, want %d", vertices, want)
			}
			if want := len(tc.segments) * 36; indexes != want {
				t.Fatalf("indexes = %d, want %d", indexes, want)
			}
		})
	}
}
