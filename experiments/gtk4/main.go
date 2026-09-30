// gtk4 is an isolated GtkGLArea capability experiment. It deliberately does
// not import DDGO or any of the other UI experiments.
package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"time"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/go-gl/gl/v3.2-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

type Point struct{ X, Y, Z float32 }

type Segment struct {
	Start, End Point
	Rapid      bool
}

type camera struct {
	target     mgl32.Vec3
	yaw, pitch float32
	distance   float32
}

func defaultCamera() camera {
	return camera{target: mgl32.Vec3{0, 0, -2}, yaw: -0.85, pitch: 0.62, distance: 155}
}

func (c camera) matrix(aspect float32) mgl32.Mat4 {
	cp := float32(math.Cos(float64(c.pitch)))
	eye := c.target.Add(mgl32.Vec3{
		c.distance * cp * float32(math.Cos(float64(c.yaw))),
		c.distance * cp * float32(math.Sin(float64(c.yaw))),
		c.distance * float32(math.Sin(float64(c.pitch))),
	})
	return mgl32.Perspective(mgl32.DegToRad(46), aspect, 0.2, 1000).Mul4(mgl32.LookAtV(eye, c.target, mgl32.Vec3{0, 0, 1}))
}

type renderer struct {
	area   *gtk.GLArea
	status *gtk.Label

	segments []Segment
	cam      camera
	width    int
	height   int

	program                    uint32
	pathCutVAO, pathCutVBO     uint32
	pathRapidVAO, pathRapidVBO uint32
	gridVAO, gridVBO           uint32
	markerVAO, markerVBO       uint32
	cutCount, rapidCount       int32
	gridCount, markerCount     int32
	initialized                bool

	pointerX, pointerY float64
	dragYaw, dragPitch float32
	dragTarget         mgl32.Vec3

	animating        bool
	animationSegment int
	animationT       float32
	lastTick         time.Time
	marker           Point
}

func main() {
	// Gtk and all OpenGL calls made by this program stay on GTK's main thread.
	runtime.LockOSThread()
	app := gtk.NewApplication("com.defcad.ddgo.gtk4capability", gio.ApplicationFlagsNone)
	app.ConnectActivate(func() { buildWindow(app) })
	os.Exit(app.Run(os.Args))
}

func buildWindow(app *gtk.Application) {
	r := &renderer{segments: makeToolpath(), cam: defaultCamera()}
	r.marker = r.segments[0].Start

	window := gtk.NewApplicationWindow(app)
	window.SetTitle("DDGO GTK4 / GtkGLArea capability experiment")
	window.SetDefaultSize(1100, 760)

	root := gtk.NewBox(gtk.OrientationVertical, 6)
	r.area = gtk.NewGLArea()
	r.area.SetHExpand(true)
	r.area.SetVExpand(true)
	r.area.SetHasDepthBuffer(true)
	// SetAllowedApis is available in the resolved gotk4 bindings. Requesting
	// desktop GL keeps the shader and go-gl/v3.2-core binding intentionally small.
	r.area.SetAllowedApis(gdk.GLAPIGL)
	r.area.SetRequiredVersion(3, 2)
	r.area.SetAutoRender(false)
	r.installLifecycle()
	r.installInput()
	root.Append(r.area)

	controls := gtk.NewBox(gtk.OrientationHorizontal, 8)
	reset := gtk.NewButtonWithLabel("Reset View")
	reset.ConnectClicked(func() {
		r.cam = defaultCamera()
		r.setStatus("view reset")
		r.area.QueueRender()
	})
	animate := gtk.NewButtonWithLabel("Animate")
	animate.ConnectClicked(func() {
		if r.animating {
			r.stopAnimation(animate)
			return
		}
		r.startAnimation(animate)
	})
	r.status = gtk.NewLabel(fmt.Sprintf("Segments: %d — waiting for GLArea", len(r.segments)))
	r.status.SetHExpand(true)
	controls.Append(reset)
	controls.Append(animate)
	controls.Append(r.status)
	root.Append(controls)
	window.SetChild(root)
	window.Present()
}

func (r *renderer) installLifecycle() {
	r.area.ConnectRealize(func() {
		r.area.MakeCurrent()
		if err := r.area.Error(); err != nil {
			log.Printf("GtkGLArea context error: %v", err)
			r.setStatus("GLArea context error: " + err.Error())
			return
		}
		if err := gl.Init(); err != nil {
			log.Printf("go-gl initialization failed: %v", err)
			r.setStatus("go-gl initialization failed: " + err.Error())
			return
		}
		if err := r.initializeGL(); err != nil {
			log.Printf("OpenGL setup failed: %v", err)
			r.setStatus("OpenGL setup failed: " + err.Error())
			return
		}
		r.initialized = true
		logGLInfo(r.area)
		r.setStatus(fmt.Sprintf("Segments: %d — GL ready; depth=%t", len(r.segments), r.area.HasDepthBuffer()))
		r.area.QueueRender()
	})

	r.area.ConnectResize(func(width, height int) {
		r.width, r.height = width, height
		if r.initialized {
			gl.Viewport(0, 0, int32(width), int32(height))
		}
		r.area.QueueRender()
	})

	r.area.ConnectRender(func(_ gdk.GLContexter) bool {
		if !r.initialized || r.width <= 0 || r.height <= 0 {
			return true
		}
		r.draw()
		return true
	})

	r.area.ConnectUnrealize(func() {
		if !r.initialized {
			return
		}
		r.area.MakeCurrent()
		r.destroyGL()
		r.initialized = false
	})
}

func (r *renderer) installInput() {
	motion := gtk.NewEventControllerMotion()
	motion.ConnectEnter(func(x, y float64) {
		r.pointerX, r.pointerY = x, y
		r.setStatus(fmt.Sprintf("Segments: %d — pointer entered %.0f,%.0f", len(r.segments), x, y))
	})
	motion.ConnectMotion(func(x, y float64) {
		r.pointerX, r.pointerY = x, y
		r.setStatus(fmt.Sprintf("Segments: %d — pointer %.0f,%.0f", len(r.segments), x, y))
	})
	motion.ConnectLeave(func() { r.setStatus(fmt.Sprintf("Segments: %d — pointer left", len(r.segments))) })
	r.area.AddController(motion)

	scroll := gtk.NewEventControllerScroll(gtk.EventControllerScrollVertical)
	scroll.ConnectScroll(func(_, dy float64) bool {
		r.cam.distance *= float32(math.Exp(float64(dy) * 0.12))
		r.cam.distance = clamp(r.cam.distance, 12, 600)
		r.setStatus(fmt.Sprintf("Segments: %d — zoom %.1f", len(r.segments), r.cam.distance))
		r.area.QueueRender()
		return true
	})
	r.area.AddController(scroll)

	orbit := gtk.NewGestureDrag()
	orbit.SetButton(1)
	orbit.ConnectDragBegin(func(_, _ float64) { r.dragYaw, r.dragPitch = r.cam.yaw, r.cam.pitch })
	orbit.ConnectDragUpdate(func(dx, dy float64) {
		r.cam.yaw = r.dragYaw + float32(dx)*0.012
		r.cam.pitch = clamp(r.dragPitch-float32(dy)*0.012, -1.45, 1.45)
		r.setStatus(fmt.Sprintf("Segments: %d — orbit", len(r.segments)))
		r.area.QueueRender()
	})
	orbit.ConnectDragEnd(func(_, _ float64) { r.setStatus(fmt.Sprintf("Segments: %d — orbit end", len(r.segments))) })
	r.area.AddController(orbit)

	pan := gtk.NewGestureDrag()
	pan.SetButton(3)
	pan.ConnectDragBegin(func(_, _ float64) { r.dragTarget = r.cam.target })
	pan.ConnectDragUpdate(func(dx, dy float64) {
		forward := viewForward(r.cam.yaw, r.cam.pitch)
		right := forward.Cross(mgl32.Vec3{0, 0, 1}).Normalize()
		up := right.Cross(forward).Normalize()
		scale := r.cam.distance * 0.0017
		r.cam.target = r.dragTarget.Sub(right.Mul(float32(dx) * scale)).Add(up.Mul(float32(dy) * scale))
		r.setStatus(fmt.Sprintf("Segments: %d — pan", len(r.segments)))
		r.area.QueueRender()
	})
	pan.ConnectDragEnd(func(_, _ float64) { r.setStatus(fmt.Sprintf("Segments: %d — pan end", len(r.segments))) })
	r.area.AddController(pan)
}

func (r *renderer) initializeGL() error {
	program, err := makeProgram(vertexShader, fragmentShader)
	if err != nil {
		return err
	}
	r.program = program
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthFunc(gl.LESS)
	gl.Enable(gl.PROGRAM_POINT_SIZE)

	var cuts, rapids []float32
	for _, s := range r.segments {
		color := [3]float32{0.15, 0.85, 0.30}
		if s.Rapid {
			color = [3]float32{1.0, 0.52, 0.12}
		}
		vertices := appendVertex(nil, s.Start, color)
		vertices = appendVertex(vertices, s.End, color)
		if s.Rapid {
			rapids = append(rapids, vertices...)
		} else {
			cuts = append(cuts, vertices...)
		}
	}
	r.pathCutVAO, r.pathCutVBO, r.cutCount = makeStaticLines(cuts)
	r.pathRapidVAO, r.pathRapidVBO, r.rapidCount = makeStaticLines(rapids)
	r.gridVAO, r.gridVBO, r.gridCount = makeStaticLines(makeGrid())
	gl.GenVertexArrays(1, &r.markerVAO)
	gl.GenBuffers(1, &r.markerVBO)
	gl.BindVertexArray(r.markerVAO)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.markerVBO)
	gl.BufferData(gl.ARRAY_BUFFER, 6*6*4, nil, gl.DYNAMIC_DRAW)
	configureVertexFormat()
	gl.BindVertexArray(0)
	return nil
}

func (r *renderer) draw() {
	gl.Viewport(0, 0, int32(r.width), int32(r.height))
	gl.ClearColor(0.035, 0.055, 0.085, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	gl.UseProgram(r.program)
	aspect := float32(r.width) / float32(r.height)
	mvp := r.cam.matrix(aspect)
	location := gl.GetUniformLocation(r.program, gl.Str("uMVP\x00"))
	gl.UniformMatrix4fv(location, 1, false, &mvp[0])
	drawLines(r.gridVAO, r.gridCount)
	drawLines(r.pathCutVAO, r.cutCount)
	drawLines(r.pathRapidVAO, r.rapidCount)
	r.uploadMarker()
	drawLines(r.markerVAO, r.markerCount)
	gl.BindVertexArray(0)
}

func (r *renderer) uploadMarker() {
	s := float32(2.4)
	p := r.marker
	data := make([]float32, 0, 6*6)
	data = appendLine(data, Point{p.X - s, p.Y, p.Z}, Point{p.X + s, p.Y, p.Z}, [3]float32{1, 1, 1})
	data = appendLine(data, Point{p.X, p.Y - s, p.Z}, Point{p.X, p.Y + s, p.Z}, [3]float32{1, 1, 1})
	data = appendLine(data, Point{p.X, p.Y, p.Z - s}, Point{p.X, p.Y, p.Z + s}, [3]float32{1, 1, 1})
	r.markerCount = int32(len(data) / 6)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.markerVBO)
	gl.BufferSubData(gl.ARRAY_BUFFER, 0, len(data)*4, gl.Ptr(data))
}

func (r *renderer) destroyGL() {
	if r.program != 0 {
		gl.DeleteProgram(r.program)
	}
	deleteBuffer := func(vao, vbo *uint32) {
		if *vbo != 0 {
			gl.DeleteBuffers(1, vbo)
			*vbo = 0
		}
		if *vao != 0 {
			gl.DeleteVertexArrays(1, vao)
			*vao = 0
		}
	}
	deleteBuffer(&r.pathCutVAO, &r.pathCutVBO)
	deleteBuffer(&r.pathRapidVAO, &r.pathRapidVBO)
	deleteBuffer(&r.gridVAO, &r.gridVBO)
	deleteBuffer(&r.markerVAO, &r.markerVBO)
	r.program = 0
}

func (r *renderer) startAnimation(button *gtk.Button) {
	r.animating, r.animationSegment, r.animationT = true, 0, 0
	r.marker = r.segments[0].Start
	r.lastTick = time.Now()
	button.SetLabel("Stop")
	r.setStatus(fmt.Sprintf("Segments: %d — animating", len(r.segments)))
	glib.TimeoutAdd(16, func() bool {
		if !r.animating {
			return false
		}
		now := time.Now()
		r.advance(float32(now.Sub(r.lastTick).Seconds()) * 55)
		r.lastTick = now
		r.area.QueueRender()
		if !r.animating {
			button.SetLabel("Animate")
			r.setStatus(fmt.Sprintf("Segments: %d — animation complete", len(r.segments)))
			return false
		}
		r.setStatus(fmt.Sprintf("Segments: %d — animation segment %d", len(r.segments), r.animationSegment+1))
		return true
	})
}

func (r *renderer) stopAnimation(button *gtk.Button) {
	r.animating = false
	button.SetLabel("Animate")
	r.setStatus(fmt.Sprintf("Segments: %d — animation stopped", len(r.segments)))
}

func (r *renderer) advance(distance float32) {
	for distance > 0 && r.animationSegment < len(r.segments) {
		s := r.segments[r.animationSegment]
		length := pointDistance(s.Start, s.End)
		remaining := (1 - r.animationT) * length
		if distance >= remaining {
			distance -= remaining
			r.animationSegment++
			r.animationT = 0
			if r.animationSegment == len(r.segments) {
				r.animating = false
				return
			}
		} else {
			r.animationT += distance / length
			distance = 0
		}
		s = r.segments[r.animationSegment]
		r.marker = lerpPoint(s.Start, s.End, r.animationT)
	}
}

func (r *renderer) setStatus(detail string) {
	if r.status != nil {
		r.status.SetText(detail)
	}
}

func makeStaticLines(data []float32) (uint32, uint32, int32) {
	var vao, vbo uint32
	gl.GenVertexArrays(1, &vao)
	gl.GenBuffers(1, &vbo)
	gl.BindVertexArray(vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(data)*4, gl.Ptr(data), gl.STATIC_DRAW)
	configureVertexFormat()
	gl.BindVertexArray(0)
	return vao, vbo, int32(len(data) / 6)
}

func configureVertexFormat() {
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 6*4, 0)
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 3, gl.FLOAT, false, 6*4, 3*4)
}

func drawLines(vao uint32, count int32) {
	if count == 0 {
		return
	}
	gl.BindVertexArray(vao)
	gl.DrawArrays(gl.LINES, 0, count)
}

func makeToolpath() []Segment {
	path := make([]Segment, 0, 3200)
	add := func(a, b Point, rapid bool) { path = append(path, Segment{a, b, rapid}) }
	current := Point{0, 0, 12}
	for _, z := range []float32{-2, -4.5, -7} {
		start := Point{-46, -36, 12}
		add(current, start, true)
		plunge := Point{start.X, start.Y, z}
		add(start, plunge, false)
		current = plunge
		for row := 0; row < 31; row++ {
			y := -36 + float32(row)*2.4
			direction := float32(1)
			if row%2 == 1 {
				direction = -1
			}
			// Split each sweep into many line segments: the renderer is proving a
			// real 3D path batch (about 3,000 segments), not a three-line demo.
			for col := 1; col <= 30; col++ {
				next := Point{-46 + direction*92*float32(col)/30, y, z}
				if direction < 0 {
					next.X = 46 - 92*float32(col)/30
				}
				add(current, next, false)
				current = next
			}
			if row < 30 {
				next := Point{current.X, y + 2.4, z}
				add(current, next, false)
				current = next
			}
		}
		add(current, Point{current.X, current.Y, 12}, true)
		current = Point{current.X, current.Y, 12}
	}
	// A deterministic helix makes the Z motion obvious independently of the raster cuts.
	add(current, Point{25, 0, 12}, true)
	current = Point{25, 0, 12}
	for i := 1; i <= 220; i++ {
		t := float32(i) / 220 * float32(6*math.Pi)
		next := Point{25 + 17*float32(math.Cos(float64(t))), 17 * float32(math.Sin(float64(t))), 8 - float32(i)*0.07}
		add(current, next, false)
		current = next
	}
	add(current, Point{current.X, current.Y, 18}, true) // final retract
	return path
}

func makeGrid() []float32 {
	data := make([]float32, 0, 600)
	gridColor := [3]float32{0.20, 0.28, 0.38}
	for i := -60; i <= 60; i += 10 {
		data = appendLine(data, Point{float32(i), -60, -8}, Point{float32(i), 60, -8}, gridColor)
		data = appendLine(data, Point{-60, float32(i), -8}, Point{60, float32(i), -8}, gridColor)
	}
	data = appendLine(data, Point{0, 0, -8}, Point{58, 0, -8}, [3]float32{1, 0.15, 0.15})
	data = appendLine(data, Point{0, 0, -8}, Point{0, 58, -8}, [3]float32{0.15, 1, 0.15})
	data = appendLine(data, Point{0, 0, -8}, Point{0, 0, 45}, [3]float32{0.25, 0.55, 1})
	return data
}

func appendLine(data []float32, a, b Point, color [3]float32) []float32 {
	return appendVertex(appendVertex(data, a, color), b, color)
}
func appendVertex(data []float32, p Point, color [3]float32) []float32 {
	return append(data, p.X, p.Y, p.Z, color[0], color[1], color[2])
}
func lerpPoint(a, b Point, t float32) Point {
	return Point{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t, a.Z + (b.Z-a.Z)*t}
}
func pointDistance(a, b Point) float32 {
	dx, dy, dz := b.X-a.X, b.Y-a.Y, b.Z-a.Z
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}
func viewForward(yaw, pitch float32) mgl32.Vec3 {
	cp := float32(math.Cos(float64(pitch)))
	return mgl32.Vec3{-cp * float32(math.Cos(float64(yaw))), -cp * float32(math.Sin(float64(yaw))), -float32(math.Sin(float64(pitch)))}
}
func clamp(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func makeProgram(vertex, fragment string) (uint32, error) {
	vs, err := compileShader(vertex, gl.VERTEX_SHADER)
	if err != nil {
		return 0, err
	}
	fs, err := compileShader(fragment, gl.FRAGMENT_SHADER)
	if err != nil {
		gl.DeleteShader(vs)
		return 0, err
	}
	program := gl.CreateProgram()
	gl.AttachShader(program, vs)
	gl.AttachShader(program, fs)
	gl.BindAttribLocation(program, 0, gl.Str("position\x00"))
	gl.BindAttribLocation(program, 1, gl.Str("color\x00"))
	gl.LinkProgram(program)
	gl.DeleteShader(vs)
	gl.DeleteShader(fs)
	var ok int32
	gl.GetProgramiv(program, gl.LINK_STATUS, &ok)
	if ok == gl.FALSE {
		return 0, fmt.Errorf("shader link: %s", programLog(program))
	}
	return program, nil
}
func compileShader(source string, kind uint32) (uint32, error) {
	shader := gl.CreateShader(kind)
	cs, free := gl.Strs(source + "\x00")
	gl.ShaderSource(shader, 1, cs, nil)
	free()
	gl.CompileShader(shader)
	var ok int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &ok)
	if ok == gl.FALSE {
		err := fmt.Errorf("shader compile: %s", shaderLog(shader))
		gl.DeleteShader(shader)
		return 0, err
	}
	return shader, nil
}
func shaderLog(shader uint32) string {
	var n int32
	gl.GetShaderiv(shader, gl.INFO_LOG_LENGTH, &n)
	b := make([]byte, n+1)
	gl.GetShaderInfoLog(shader, n, nil, &b[0])
	return string(b)
}
func programLog(program uint32) string {
	var n int32
	gl.GetProgramiv(program, gl.INFO_LOG_LENGTH, &n)
	b := make([]byte, n+1)
	gl.GetProgramInfoLog(program, n, nil, &b[0])
	return string(b)
}
func logGLInfo(area *gtk.GLArea) {
	version := gl.GoStr(gl.GetString(gl.VERSION))
	vendor := gl.GoStr(gl.GetString(gl.VENDOR))
	renderer := gl.GoStr(gl.GetString(gl.RENDERER))
	glsl := gl.GoStr(gl.GetString(gl.SHADING_LANGUAGE_VERSION))
	var depth int32
	gl.GetIntegerv(gl.DEPTH_BITS, &depth)
	log.Printf("GtkGLArea api=%s depth-requested=%t depth-bits=%d GL_VERSION=%q GL_VENDOR=%q GL_RENDERER=%q GLSL=%q", area.API(), area.HasDepthBuffer(), depth, version, vendor, renderer, glsl)
}

const vertexShader = `#version 150
in vec3 position;
in vec3 color;
out vec3 vColor;
uniform mat4 uMVP;
void main() { vColor = color; gl_Position = uMVP * vec4(position, 1.0); }
`
const fragmentShader = `#version 150
in vec3 vColor;
out vec4 outputColor;
void main() { outputColor = vec4(vColor, 1.0); }
`
