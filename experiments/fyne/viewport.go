package main

import (
	"fmt"
	"image/color"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

const diagnosticSegments = 48

// Viewport is deliberately a normal public Fyne widget. Its input handling is
// the Phase-1 proof; its lines are a deliberately tiny CPU projection diagnostic,
// not an attempted toolpath renderer.
type Viewport struct {
	widget.BaseWidget

	path      []Segment
	shader    *canvas.Shader
	info      *canvas.Text
	caption   *canvas.Text
	lines     []*canvas.Line
	objects   []fyne.CanvasObject
	animation *fyne.Animation

	yaw, pitch, zoom float32
	panX, panY       float32
	button           desktop.MouseButton
	presses          int
	releases         int
	dragX, dragY     float32
	wheel            float32
	hover            fyne.Position
	animating        bool

	// OnInput is called only from Fyne's input callbacks, so no goroutine or
	// fyne.Do call is needed for these UI updates.
	OnInput func(string)
}

var (
	_ desktop.Mouseable = (*Viewport)(nil)
	_ desktop.Hoverable = (*Viewport)(nil)
	_ fyne.Draggable    = (*Viewport)(nil)
	_ fyne.Scrollable   = (*Viewport)(nil)
)

func NewViewport(path []Segment) *Viewport {
	v := &Viewport{path: path}
	v.ResetView()
	v.ExtendBaseWidget(v)
	v.shader = canvas.NewShader("ddgo-fyne-procedural-diagnostic", []byte(viewportShaderDesktop), []byte(viewportShaderES))
	v.shader.Uniforms = map[string]float32{"yaw": v.yaw, "pitch": v.pitch, "zoom": v.zoom}
	v.info = canvas.NewText("Viewport input\nwaiting for mouse input", color.White)
	v.info.TextSize = 14
	v.caption = canvas.NewText("Phase 2: fixed procedural shader + 48-line CPU projection diagnostic (not a toolpath renderer)", color.RGBA{R: 185, G: 215, B: 230, A: 255})
	v.caption.TextSize = 12

	count := diagnosticSegments
	if len(path) < count {
		count = len(path)
	}
	v.lines = make([]*canvas.Line, count)
	v.objects = []fyne.CanvasObject{v.shader}
	for i := range v.lines {
		line := canvas.NewLine(color.RGBA{R: 105, G: 235, B: 155, A: 190})
		line.StrokeWidth = 1.5
		if path[i].Rapid {
			line.StrokeColor = color.RGBA{R: 245, G: 170, B: 70, A: 190}
		}
		v.lines[i] = line
		v.objects = append(v.objects, line)
	}
	v.objects = append(v.objects, v.info, v.caption)
	return v
}

func (v *Viewport) CreateRenderer() fyne.WidgetRenderer {
	v.ExtendBaseWidget(v)
	return &viewportRenderer{viewport: v}
}

func (v *Viewport) MouseDown(e *desktop.MouseEvent) {
	v.button = e.Button
	v.presses++
	v.hover = e.Position
	v.emit("mouse press")
}

func (v *Viewport) MouseUp(e *desktop.MouseEvent) {
	v.button = 0
	v.releases++
	v.hover = e.Position
	v.emit("mouse release")
}

func (v *Viewport) MouseIn(e *desktop.MouseEvent) {
	v.hover = e.Position
	v.emit("mouse entered viewport")
}

func (v *Viewport) MouseMoved(e *desktop.MouseEvent) {
	v.hover = e.Position
	v.updateInfo()
}

func (v *Viewport) MouseOut() {
	v.emit("mouse left viewport")
}

func (v *Viewport) Dragged(e *fyne.DragEvent) {
	v.dragX += e.Dragged.DX
	v.dragY += e.Dragged.DY
	if v.button == desktop.MouseButtonSecondary || v.button == desktop.MouseButtonTertiary {
		v.panX += e.Dragged.DX
		v.panY += e.Dragged.DY
	} else {
		v.yaw += e.Dragged.DX * 0.012
		v.pitch += e.Dragged.DY * 0.012
	}
	v.updateScene()
	v.emit("drag")
}

func (v *Viewport) DragEnd() {
	v.emit("drag end")
}

func (v *Viewport) Scrolled(e *fyne.ScrollEvent) {
	v.wheel += e.Scrolled.DY
	v.zoom *= float32(math.Exp(float64(e.Scrolled.DY) * 0.08))
	if v.zoom < 0.25 {
		v.zoom = 0.25
	}
	if v.zoom > 3 {
		v.zoom = 3
	}
	v.updateScene()
	v.emit("mouse wheel")
}

func (v *Viewport) ResetView() {
	v.yaw, v.pitch, v.zoom = 0.55, -0.45, 0.9
	v.panX, v.panY = 0, 0
	v.dragX, v.dragY, v.wheel = 0, 0, 0
	v.updateScene()
}

// ToggleShaderAnimation only animates the fixed diagnostic shader. It is
// intentionally not presented as traversal of the generated CNC segments.
func (v *Viewport) ToggleShaderAnimation() bool {
	if v.animation == nil {
		v.animation = canvas.NewShaderAnimation(v.shader)
	}
	v.animating = !v.animating
	if v.animating {
		v.animation.Start()
	} else {
		v.animation.Stop()
	}
	return v.animating
}

func (v *Viewport) updateScene() {
	if v.shader == nil {
		return
	}
	v.shader.Uniforms["yaw"] = v.yaw
	v.shader.Uniforms["pitch"] = v.pitch
	v.shader.Uniforms["zoom"] = v.zoom
	v.shader.Refresh()
	v.reproject()
	v.updateInfo()
}

func (v *Viewport) reproject() {
	if len(v.lines) == 0 || v.Size().Width < 1 || v.Size().Height < 1 {
		return
	}
	for i, line := range v.lines {
		line.Position1 = v.project(v.path[i].Start)
		line.Position2 = v.project(v.path[i].End)
		line.Refresh()
	}
}

// project is intentionally the entire CPU diagnostic: rotate/project 48 lines
// on every camera update, with no clipping or depth sorting.
func (v *Viewport) project(p Point) fyne.Position {
	sy, cy := float32(math.Sin(float64(v.yaw))), float32(math.Cos(float64(v.yaw)))
	sp, cp := float32(math.Sin(float64(v.pitch))), float32(math.Cos(float64(v.pitch)))
	x := cy*p.X - sy*p.Y
	y := sy*p.X + cy*p.Y
	z := p.Z
	y, z = cp*y-sp*z, sp*y+cp*z
	scale := v.Size().Height * 0.010 * v.zoom / (1 + z*0.004)
	return fyne.NewPos(v.Size().Width/2+x*scale+v.panX, v.Size().Height/2-y*scale+v.panY)
}

func (v *Viewport) updateInfo() {
	if v.info == nil {
		return
	}
	v.info.Text = fmt.Sprintf("Viewport input\npress/release: %d / %d\ndrag: %.0f, %.0f\nwheel: %.0f\nhover: %.0f, %.0f", v.presses, v.releases, v.dragX, v.dragY, v.wheel, v.hover.X, v.hover.Y)
	v.info.Refresh()
}

func (v *Viewport) emit(event string) {
	v.updateInfo()
	if v.OnInput != nil {
		v.OnInput(fmt.Sprintf("Viewport %s — press %d, release %d, drag %.0f, %.0f, wheel %.0f", event, v.presses, v.releases, v.dragX, v.dragY, v.wheel))
	}
}

type viewportRenderer struct{ viewport *Viewport }

func (r *viewportRenderer) Destroy() {}

func (r *viewportRenderer) Layout(size fyne.Size) {
	v := r.viewport
	v.shader.Resize(size)
	v.info.Move(fyne.NewPos(12, 12))
	v.caption.Move(fyne.NewPos(12, size.Height-22))
	v.reproject()
}

func (r *viewportRenderer) MinSize() fyne.Size { return fyne.NewSize(480, 320) }

func (r *viewportRenderer) Objects() []fyne.CanvasObject { return r.viewport.objects }

func (r *viewportRenderer) Refresh() { r.viewport.updateScene() }
