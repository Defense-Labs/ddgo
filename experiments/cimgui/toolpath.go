package main

import "math"

// Point and Segment intentionally duplicate the small synthetic data model used
// by the other UI spikes. This experiment must remain self-contained.
type Point struct{ X, Y, Z float32 }

type Segment struct {
	Start, End Point
	Rapid      bool
}

func buildToolpath() []Segment {
	var path []Segment
	current := Point{X: -42, Y: -30, Z: 12}
	move := func(next Point, rapid bool) {
		path = append(path, Segment{Start: current, End: next, Rapid: rapid})
		current = next
	}

	// Three depth passes over six circular pockets make the depth relationships
	// obvious, while the deterministic small steps yield a real line workload.
	centres := []Point{{-28, -18, 0}, {0, -18, 0}, {28, -18, 0}, {-28, 18, 0}, {0, 18, 0}, {28, 18, 0}}
	depths := []float32{-2, -4, -6}
	for _, depth := range depths {
		for i, centre := range centres {
			radius := float32(7 + i%3)
			move(Point{X: centre.X + radius, Y: centre.Y, Z: 12}, true)
			move(Point{X: centre.X + radius, Y: centre.Y, Z: depth}, false) // plunge
			for step := 1; step <= 120; step++ {
				a := 2 * math.Pi * float64(step) / 120
				// A slight Z modulation makes each circle look helical rather
				// than like a flat CAD sketch.
				z := depth - float32(step)/120*0.55
				move(Point{X: centre.X + radius*float32(math.Cos(a)), Y: centre.Y + radius*float32(math.Sin(a)), Z: z}, false)
			}
			move(Point{X: current.X, Y: current.Y, Z: 12}, false) // retract
		}
	}

	// A zig-zag facing pass supplies long straight XY cutting moves and brings
	// the generated total to 2,397 segments including rapid repositioning.
	move(Point{X: -42, Y: -35, Z: 12}, true)
	move(Point{X: -42, Y: -35, Z: -1}, false)
	for row := 0; row < 180; row++ {
		y := -35 + float32(row)*70/179
		if row%2 == 0 {
			move(Point{X: 42, Y: y, Z: -1}, false)
		} else {
			move(Point{X: -42, Y: y, Z: -1}, false)
		}
	}
	move(Point{X: current.X, Y: current.Y, Z: 15}, false) // final retract
	return path
}

func pointLerp(a, b Point, t float32) Point {
	return Point{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t, a.Z + (b.Z-a.Z)*t}
}
