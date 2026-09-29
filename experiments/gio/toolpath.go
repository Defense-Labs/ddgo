package main

import "math"

type Point struct{ X, Y, Z float32 }
type Segment struct {
	Start, End Point
	Rapid      bool
}

// makeToolpath deliberately duplicates the other experiments' synthetic data.
// It is deterministic, contains 2,100+ segments, and does not parse G-code.
func makeToolpath() []Segment {
	var path []Segment
	p := Point{X: -58, Y: -42, Z: 25}
	move := func(to Point, rapid bool) { path = append(path, Segment{p, to, rapid}); p = to }
	for level := 0; level < 8; level++ {
		z := float32(-2 - level*2)
		move(Point{-58, -42, 20}, true)
		move(Point{-58, -42, z}, false) // plunge
		// A rectangular pocket, repeated to give visibly dense XY cutting motion.
		for pass := 0; pass < 5; pass++ {
			inset := float32(pass * 4)
			move(Point{-58 + inset, -42 + inset, z}, false)
			move(Point{58 - inset, -42 + inset, z}, false)
			move(Point{58 - inset, 42 - inset, z}, false)
			move(Point{-58 + inset, 42 - inset, z}, false)
			move(Point{-58 + inset, -42 + inset, z}, false)
		}
		// A circular/helical-looking feature. Its rising Z makes depth obvious.
		center := Point{X: 0, Y: 0, Z: z}
		for i := 0; i <= 256; i++ {
			a := 2 * math.Pi * float64(i) / 256
			move(Point{X: center.X + 25*float32(math.Cos(a)), Y: center.Y + 25*float32(math.Sin(a)), Z: z + float32(i)*0.015}, false)
		}
		move(Point{p.X, p.Y, 20}, false) // retract
	}
	move(Point{70, -55, 25}, true)
	return path
}

func lerpSegment(s Segment, t float32) Point {
	return Point{X: s.Start.X + (s.End.X-s.Start.X)*t, Y: s.Start.Y + (s.End.Y-s.Start.Y)*t, Z: s.Start.Z + (s.End.Z-s.Start.Z)*t}
}
