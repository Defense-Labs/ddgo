// Cogent Core / xyz Stage-1 capability experiment. It is intentionally
// standalone and does not import DDGO or any other experiment.
package main

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"cogentcore.org/core/core"
	"cogentcore.org/core/events"
	"cogentcore.org/core/math32"
	"cogentcore.org/core/styles"
	"cogentcore.org/core/styles/units"
	"cogentcore.org/core/tree"
	"cogentcore.org/core/xyz"
	"cogentcore.org/core/xyz/xyzcore"
)

// Point is in CNC coordinates: X/Y are the work plane and +Z is upward.
type Point struct{ X, Y, Z float32 }

type Segment struct {
	Start, End Point
	Rapid      bool
}

type app struct {
	path       []Segment
	view       *xyzcore.Scene
	scene      *xyz.Scene
	marker     *xyz.Solid
	animateBtn *core.Button
	status     *core.Text

	animating bool
	segment   int
	segmentT  float32
}

func main() {
	a := newApp()
	b := core.NewBody("Cogent Core 3D Test")

	// The ordinary Core widget and the 3D widget deliberately share one body.
	a.view = xyzcore.NewScene(b)
	a.view.Styler(func(s *styles.Style) {
		s.Grow.Set(1, 1)
		s.Min.Set(units.Dp(320), units.Dp(320))
	})
	a.scene = a.view.SceneXYZ()
	a.configureScene()

	controls := core.NewFrame(b)
	controls.Styler(func(s *styles.Style) {
		s.Direction = styles.Row
		s.Gap.Set(units.Dp(12))
		s.Padding.Set(units.Dp(10))
	})
	core.NewButton(controls).SetText("Reset View").OnClick(func(events.Event) {
		_ = a.scene.SetCamera("ddgo-default")
		a.view.NeedsRender()
	})
	a.animateBtn = core.NewButton(controls).SetText("Animate")
	a.animateBtn.OnClick(func(events.Event) { a.toggleAnimation() })
	a.status = core.NewText(controls).SetText(a.statusText())
	a.status.Styler(func(s *styles.Style) { s.Grow.Set(1, 0) })

	// This is Core's regular scene animation facility. Dt is milliseconds in
	// v0.3.42, so advancePlayback converts it to seconds exactly once.
	a.view.Animate(func(animation *core.Animation) {
		if !a.animating {
			return
		}
		a.advancePlayback(animation.Dt / 1000)
		a.view.NeedsRender()
	})
	b.RunMainWindow()
}

func newApp() *app {
	path := makeToolpath()
	if len(path) == 0 {
		panic("synthetic path is empty")
	}
	return &app{path: path}
}

func (a *app) configureScene() {
	sc := a.scene
	sc.Background = image.NewUniform(color.RGBA{18, 24, 32, 255})
	xyz.NewAmbient(sc, "ambient", 0.55, xyz.DirectSun)
	xyz.NewDirectional(sc, "sun", 0.85, xyz.DirectSun).Pos.Set(-0.4, 1, 0.7)

	// CNC X -> world X, CNC Y -> world Z, CNC Z -> world Y. World +Y is
	// Cogent Core's camera-up convention, therefore positive CNC Z is up.
	cut, rapid := splitPath(a.path)
	sc.SetMesh(prismMesh("cutting-toolpath", cut, 0.42))
	sc.SetMesh(prismMesh("rapid-toolpath", rapid, 0.24))
	sc.SetMesh(prismMesh("work-grid", gridSegments(), 0.065))
	sc.SetMesh(prismMesh("axis-x", []Segment{{Start: Point{-58, 0, 0}, End: Point{58, 0, 0}}}, 0.34))
	sc.SetMesh(prismMesh("axis-y", []Segment{{Start: Point{0, -43, 0}, End: Point{0, 43, 0}}}, 0.34))
	sc.SetMesh(prismMesh("axis-z", []Segment{{Start: Point{0, 0, -10}, End: Point{0, 0, 22}}}, 0.34))

	tree.AddChild(sc, func(n *xyz.Solid) {
		n.SetName("cutting-path")
		n.SetMeshName("cutting-toolpath")
		n.SetColor(color.RGBA{58, 220, 166, 255}).SetEmissive(color.RGBA{8, 30, 22, 255})
	})
	tree.AddChild(sc, func(n *xyz.Solid) {
		n.SetName("rapid-path")
		n.SetMeshName("rapid-toolpath")
		n.SetColor(color.RGBA{255, 156, 58, 255}).SetEmissive(color.RGBA{30, 16, 4, 255})
	})
	tree.AddChild(sc, func(n *xyz.Solid) {
		n.SetName("xy-grid")
		n.SetMeshName("work-grid")
		n.SetColor(color.RGBA{65, 86, 101, 255})
	})
	for _, axis := range []struct {
		mesh string
		clr  color.RGBA
	}{
		{"axis-x", color.RGBA{232, 77, 77, 255}},
		{"axis-y", color.RGBA{86, 212, 99, 255}},
		{"axis-z", color.RGBA{80, 148, 255, 255}},
	} {
		axis := axis
		tree.AddChildAt(sc, axis.mesh+"-solid", func(n *xyz.Solid) {
			n.SetName(axis.mesh + "-solid")
			n.SetMeshName(axis.mesh)
			n.SetColor(axis.clr).SetEmissive(axis.clr)
		})
	}

	markerMesh := xyz.NewSphere(sc, "tool-marker", 1.35, 18)
	tree.AddChild(sc, func(n *xyz.Solid) {
		n.SetName("tool-position")
		n.SetMesh(markerMesh).SetColor(color.RGBA{255, 238, 92, 255}).SetEmissive(color.RGBA{90, 72, 8, 255})
		n.SetPosePos(world(a.path[0].Start))
		a.marker = n
	})

	// A perspective camera with CNC Z/world Y physically upward.
	sc.Camera.FOV = 38
	sc.Camera.Near = 0.1
	sc.Camera.Far = 1000
	sc.Camera.Pose.Pos.Set(112, 94, 124)
	sc.Camera.LookAt(math32.Vec3(0, -2, 0), math32.Vec3(0, 1, 0))
	sc.SaveCamera("ddgo-default")
}

func (a *app) toggleAnimation() {
	a.animating = !a.animating
	if a.animating {
		a.animateBtn.SetText("Stop")
	} else {
		a.animateBtn.SetText("Animate")
	}
	a.animateBtn.UpdateRender()
	a.view.NeedsRender()
}

func (a *app) advancePlayback(dt float32) {
	// 55 work units/second makes a full traversal visibly demonstrative
	// without treating animation as a rendering benchmark.
	remaining := float32(55) * dt
	oldSegment := a.segment
	for remaining > 0 {
		seg := a.path[a.segment]
		length := distance(seg.Start, seg.End)
		if length <= 0.0001 {
			a.nextSegment()
			continue
		}
		left := (1 - a.segmentT) * length
		if remaining < left {
			a.segmentT += remaining / length
			remaining = 0
		} else {
			remaining -= left
			a.nextSegment()
		}
	}
	seg := a.path[a.segment]
	a.marker.SetPosePos(world(lerp(seg.Start, seg.End, a.segmentT)))
	if a.segment != oldSegment {
		a.status.SetText(a.statusText())
		a.status.UpdateRender()
	}
}

func (a *app) nextSegment() {
	a.segment++
	a.segmentT = 0
	if a.segment >= len(a.path) {
		a.segment = 0
	}
}

func (a *app) statusText() string {
	return fmt.Sprintf("Segments: %d   Current: %d   drag orbit | Shift+drag pan | scroll zoom", len(a.path), a.segment+1)
}

func splitPath(path []Segment) (cut, rapid []Segment) {
	for _, s := range path {
		if s.Rapid {
			rapid = append(rapid, s)
		} else {
			cut = append(cut, s)
		}
	}
	return cut, rapid
}

// makeToolpath is deterministic and intentionally duplicated in this spike.
// It produces cutting raster passes at four depths, circular pockets, plunges,
// retracts, rapid repositioning, a variable-Z spiral, and a final retract.
func makeToolpath() []Segment {
	var out []Segment
	pos := Point{-52, -38, 12}
	move := func(to Point, rapid bool) {
		out = append(out, Segment{Start: pos, End: to, Rapid: rapid})
		pos = to
	}
	depths := []float32{-1.5, -3.5, -5.5, -7.5}
	for pass, z := range depths {
		move(Point{-46, -32, 8}, true)
		move(Point{-46, -32, z}, false) // plunge
		const rows = 160
		for row := 0; row < rows; row++ {
			y := float32(-32) + float32(row)*64/float32(rows-1)
			if row%2 == 0 {
				move(Point{46, y, z}, false)
			} else {
				move(Point{-46, y, z}, false)
			}
		}
		move(Point{pos.X, pos.Y, 8}, false) // retract

		move(Point{41, 0, 8}, true)
		move(Point{41, 0, z}, false) // plunge into a circular feature
		for i := 1; i <= 112; i++ {
			ang := float64(i) * 2 * math.Pi / 112
			move(Point{25 + 16*float32(math.Cos(ang)), 16 * float32(math.Sin(ang)), z}, false)
		}
		move(Point{pos.X, pos.Y, 8}, false)
		if pass < len(depths)-1 {
			move(Point{-28, 20, 8}, true)
		}
	}

	move(Point{-20, 0, 8}, true)
	move(Point{-20, 0, -7.5}, false)
	for i := 1; i <= 192; i++ {
		t := float32(i) / 192
		ang := float64(t * 6 * math.Pi)
		r := 5 + 14*t
		move(Point{-20 + r*float32(math.Cos(ang)), r * float32(math.Sin(ang)), -7.5 + 10*t}, false)
	}
	move(Point{pos.X, pos.Y, 14}, false) // final retract
	return out
}

func gridSegments() []Segment {
	grid := make([]Segment, 0, 34)
	for i := -8; i <= 8; i++ {
		v := float32(i * 6)
		grid = append(grid,
			Segment{Start: Point{v, -42, 0}, End: Point{v, 42, 0}},
			Segment{Start: Point{-54, v * 0.78, 0}, End: Point{54, v * 0.78, 0}},
		)
	}
	return grid
}

// prismMesh makes one indexed triangle mesh for all supplied segments. Each
// segment has six flat-shaded faces (24 vertices, 36 indexes): there are never
// per-segment xyz.Solid nodes.
func prismMesh(name string, segments []Segment, width float32) *xyz.GenMesh {
	mesh := &xyz.GenMesh{MeshBase: xyz.MeshBase{Name: name}}
	mesh.Vertex = make(math32.ArrayF32, 0, len(segments)*24*3)
	mesh.Normal = make(math32.ArrayF32, 0, len(segments)*24*3)
	mesh.TexCoord = make(math32.ArrayF32, 0, len(segments)*24*2)
	mesh.Index = make(math32.ArrayU32, 0, len(segments)*36)
	for _, segment := range segments {
		addPrism(mesh, world(segment.Start), world(segment.End), width*0.5)
	}
	return mesh
}

type vec struct{ x, y, z float32 }

func (v vec) add(w vec) vec       { return vec{v.x + w.x, v.y + w.y, v.z + w.z} }
func (v vec) sub(w vec) vec       { return vec{v.x - w.x, v.y - w.y, v.z - w.z} }
func (v vec) scale(s float32) vec { return vec{v.x * s, v.y * s, v.z * s} }
func (v vec) dot(w vec) float32   { return v.x*w.x + v.y*w.y + v.z*w.z }
func (v vec) cross(w vec) vec {
	return vec{v.y*w.z - v.z*w.y, v.z*w.x - v.x*w.z, v.x*w.y - v.y*w.x}
}
func (v vec) normal() vec {
	l := float32(math.Sqrt(float64(v.dot(v))))
	if l == 0 {
		return vec{}
	}
	return v.scale(1 / l)
}

func addPrism(mesh *xyz.GenMesh, start, end math32.Vector3, halfWidth float32) {
	p := vec{start.X, start.Y, start.Z}
	q := vec{end.X, end.Y, end.Z}
	d := q.sub(p)
	if d.dot(d) < 0.000001 {
		return
	}
	d = d.normal()
	// Select the world axis least parallel to the segment direction.
	ref := vec{1, 0, 0}
	if abs(d.y) < abs(d.x) && abs(d.y) <= abs(d.z) {
		ref = vec{0, 1, 0}
	} else if abs(d.z) < abs(d.x) && abs(d.z) < abs(d.y) {
		ref = vec{0, 0, 1}
	}
	sideA := d.cross(ref).normal().scale(halfWidth)
	sideB := d.cross(sideA).normal().scale(halfWidth)
	corners := [8]vec{
		p.sub(sideA).sub(sideB), p.add(sideA).sub(sideB), p.add(sideA).add(sideB), p.sub(sideA).add(sideB),
		q.sub(sideA).sub(sideB), q.add(sideA).sub(sideB), q.add(sideA).add(sideB), q.sub(sideA).add(sideB),
	}
	faces := [][4]int{{0, 3, 2, 1}, {4, 5, 6, 7}, {0, 1, 5, 4}, {1, 2, 6, 5}, {2, 3, 7, 6}, {3, 0, 4, 7}}
	for _, face := range faces {
		base := uint32(len(mesh.Vertex) / 3)
		normal := corners[face[1]].sub(corners[face[0]]).cross(corners[face[2]].sub(corners[face[0]])).normal()
		for _, index := range face {
			v := corners[index]
			mesh.Vertex = append(mesh.Vertex, v.x, v.y, v.z)
			mesh.Normal = append(mesh.Normal, normal.x, normal.y, normal.z)
			mesh.TexCoord = append(mesh.TexCoord, 0, 0)
		}
		mesh.Index = append(mesh.Index, base, base+1, base+2, base, base+2, base+3)
	}
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func world(p Point) math32.Vector3 { return math32.Vec3(p.X, p.Z, p.Y) }

func distance(a, b Point) float32 {
	dx, dy, dz := a.X-b.X, a.Y-b.Y, a.Z-b.Z
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

func lerp(a, b Point, t float32) Point {
	return Point{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t, a.Z + (b.Z-a.Z)*t}
}
