package main

import "math"

// Point and Segment deliberately duplicate the small experiment-only data model.
// No DDGO packages or file formats are used here.
type Point struct {
	X, Y, Z float32
}

type Segment struct {
	Start, End Point
	Rapid      bool
}

// syntheticToolpath creates a deterministic, CNC-like path with 2,900-ish
// segments. It is retained even though the public Fyne APIs cannot submit this
// geometry to a GPU line pipeline.
func syntheticToolpath() []Segment {
	path := make([]Segment, 0, 3000)
	current := Point{X: 0, Y: 0, Z: 12}
	move := func(next Point, rapid bool) {
		path = append(path, Segment{Start: current, End: next, Rapid: rapid})
		current = next
	}

	move(Point{X: -48, Y: -38, Z: 12}, true)
	for layer, z := range []float32{-1.5, -3, -4.5, -6} {
		move(Point{X: current.X, Y: current.Y, Z: z}, false) // plunge
		for row := 0; row < 32; row++ {
			y := float32(-38 + row*2)
			left, right := float32(-48), float32(48)
			if (row+layer)%2 != 0 {
				left, right = right, left
			}
			move(Point{X: left, Y: y, Z: z}, true) // rapid/reposition
			for step := 1; step <= 20; step++ {
				x := left + (right-left)*float32(step)/20
				move(Point{X: x, Y: y, Z: z}, false)
			}
		}
	}

	// A rising circle gives the data a clearly non-planar, helical-looking feature.
	move(Point{X: 18, Y: 0, Z: 8}, true)
	for i := 1; i <= 240; i++ {
		a := 2 * math.Pi * float64(i) / 80
		move(Point{
			X: 18 + 16*float32(math.Cos(a)),
			Y: 16 * float32(math.Sin(a)),
			Z: -6 + float32(i)*0.045,
		}, false)
	}
	move(Point{X: current.X, Y: current.Y, Z: 12}, true) // final retract
	return path
}
