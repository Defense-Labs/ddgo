package main

import (
	"context"
	"math"
)

// Point uses conventional CNC coordinates: X/Y are the table plane and Z is up.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// Segment is one deterministic motion of the synthetic CNC demonstration path.
// Rapid distinguishes non-cutting positioning moves from cutting moves.
type Segment struct {
	Start Point `json:"start"`
	End   Point `json:"end"`
	Rapid bool  `json:"rapid"`
}

// App owns the Wails binding for this deliberately isolated capability spike.
type App struct{ ctx context.Context }

func NewApp() *App                         { return &App{} }
func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// GetToolpath transfers the complete, Go-generated path once through Wails.
// The frontend uses the returned data for both static GPU geometry and local
// marker playback; it never asks Go for per-frame animation updates.
func (a *App) GetToolpath() []Segment {
	segments := make([]Segment, 0, 3400)
	current := Point{X: 0, Y: 0, Z: 12}
	move := func(end Point, rapid bool) {
		segments = append(segments, Segment{Start: current, End: end, Rapid: rapid})
		current = end
	}
	rapid := func(x, y, z float64) { move(Point{X: x, Y: y, Z: z}, true) }
	cut := func(x, y, z float64) { move(Point{X: x, Y: y, Z: z}, false) }

	const safeZ = 12.0
	// Three pocketing passes provide dense XY cutting with plunges and retracts.
	for _, depth := range []float64{-1.5, -3.0, -4.5} {
		rapid(-52, -32, safeZ)
		cut(-52, -32, depth) // plunge
		for row := 0; row <= 96; row++ {
			y := -32.0 + float64(row)*(64.0/96.0)
			if row%2 == 0 {
				cut(52, y, depth)
			} else {
				cut(-52, y, depth)
			}
		}
		rapid(current.X, current.Y, safeZ) // retract between depths
	}

	// A circular-looking profile demonstrates arc approximation at two depths.
	for _, depth := range []float64{-2.0, -5.5} {
		const centerX, centerY, radius = 22.0, 0.0, 17.0
		rapid(centerX+radius, centerY, safeZ)
		cut(centerX+radius, centerY, depth)
		for step := 1; step <= 360; step++ {
			angle := float64(step) * 2 * math.Pi / 360
			cut(centerX+radius*math.Cos(angle), centerY+radius*math.Sin(angle), depth)
		}
		rapid(current.X, current.Y, safeZ)
	}

	// A shallow wavy finishing pass makes the changing Z coordinate conspicuous.
	for _, baseDepth := range []float64{-1.0, -3.5, -5.0} {
		rapid(-55, 42, safeZ)
		cut(-55, 42, baseDepth)
		for step := 1; step <= 260; step++ {
			x := -55.0 + float64(step)*(110.0/260.0)
			y := 42.0 + 8.0*math.Sin(float64(step)*2*math.Pi/52.0)
			z := baseDepth + 1.2*math.Sin(float64(step)*2*math.Pi/104.0)
			cut(x, y, z)
		}
		rapid(current.X, current.Y, safeZ)
	}

	// The explicit final retract is the end state of the simulated program.
	rapid(0, 0, safeZ)
	return segments
}
