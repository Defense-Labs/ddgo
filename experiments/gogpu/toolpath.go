package main

import "math"

// Point is a machine-space point in millimetres.
type Point struct{ X, Y, Z float32 }

// Segment is one deterministic synthetic CNC move.
type Segment struct {
	Start, End Point
	Rapid      bool
}

func makeToolpath() []Segment {
	var path []Segment
	current := Point{0, 0, 20}
	add := func(end Point, rapid bool) {
		path = append(path, Segment{Start: current, End: end, Rapid: rapid})
		current = end
	}
	line := func(end Point, rapid bool, pieces int) {
		start := current
		for i := 1; i <= pieces; i++ {
			t := float32(i) / float32(pieces)
			add(Point{
				X: start.X + (end.X-start.X)*t,
				Y: start.Y + (end.Y-start.Y)*t,
				Z: start.Z + (end.Z-start.Z)*t,
			}, rapid)
		}
	}

	// Four raster pockets produce 1,500+ individual cutting segments and
	// clearly expose several cutting depths.
	for level, z := range []float32{-2, -5, -8, -11} {
		startX := float32(-60)
		if level%2 != 0 {
			startX = 60
		}
		add(Point{X: current.X, Y: current.Y, Z: 18}, true) // retract
		add(Point{X: startX, Y: -55, Z: 18}, true)          // reposition
		add(Point{X: startX, Y: -55, Z: z}, false)          // plunge
		for row := 0; row < 23; row++ {
			y := float32(-55 + row*5)
			x := float32(60)
			if (row+level)%2 != 0 {
				x = -60
			}
			line(Point{X: x, Y: y, Z: z}, false, 16)
			if row < 22 {
				add(Point{X: x, Y: y + 5, Z: z}, false)
			}
		}
	}

	// A small descending helix gives the scene an unmistakable curved 3D area.
	add(Point{X: current.X, Y: current.Y, Z: 18}, true)
	add(Point{X: 34, Y: 0, Z: 18}, true)
	add(Point{X: 34, Y: 0, Z: -2}, false)
	const turns, steps = 3, 360
	for i := 1; i <= steps; i++ {
		a := float64(i) / float64(steps) * float64(turns) * 2 * math.Pi
		t := float32(i) / float32(steps)
		add(Point{
			X: 14 + 20*float32(math.Cos(a)),
			Y: 20 * float32(math.Sin(a)),
			Z: -2 - 10*t,
		}, false)
	}

	add(Point{X: current.X, Y: current.Y, Z: 20}, true) // final retract
	return path
}
