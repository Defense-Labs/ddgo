package main

import (
	"fmt"
	"math"
	"runtime"

	"github.com/AllenDang/cimgui-go/backend"
	"github.com/AllenDang/cimgui-go/backend/glfwbackend"
	"github.com/AllenDang/cimgui-go/imgui"
	_ "github.com/AllenDang/cimgui-go/impl/glfw" // cimgui-go's GLFW input implementation
)

func init() { runtime.LockOSThread() }

type app struct {
	path        []Segment
	renderer    renderer
	camera      camera
	animating   bool
	progress    float32
	tool        Point
	markerDirty bool

	hovered, active, dragging bool
	lastDelta, lastWheel      imgui.Vec2
	fps                       float32
}

func main() {
	a := &app{path: buildToolpath(), camera: newCamera()}
	if len(a.path) == 0 {
		panic("synthetic toolpath is empty")
	}
	a.tool = a.path[0].Start
	a.markerDirty = true

	// This is deliberately the backend-owned GLFW lifecycle. No second GLFW Go
	// binding is imported or initialized by this experiment.
	b, err := backend.CreateBackend(glfwbackend.NewGLFWBackend())
	if err != nil {
		panic(fmt.Sprintf("create cimgui-go GLFW backend: %v", err))
	}
	b.SetAfterCreateContextHook(func() {
		if err := a.renderer.initialize(a.path); err != nil {
			panic(err)
		}
		fmt.Printf("OpenGL: %s | renderer: %s | default depth bits: %d\n", a.renderer.glVersion, a.renderer.glRenderer, a.renderer.depthBits)
	})
	b.SetBeforeDestroyContextHook(a.renderer.destroy)
	b.SetBgColor(imgui.NewVec4(0.08, 0.09, 0.11, 1))
	b.CreateWindow("DDGO cimgui-go UI / 3D capability experiment", 1000, 750)
	// The current GLFW backend turns these on during CreateWindow. They caused
	// this single-window spike's root UI to appear as a detached platform pane
	// on this host, so deliberately disable them rather than exercise them.
	io := imgui.CurrentIO()
	io.SetConfigFlags(io.ConfigFlags() &^ (imgui.ConfigFlagsDockingEnable | imgui.ConfigFlagsViewportsEnable))
	b.SetTargetFPS(60)
	b.SetCloseCallback(func() { fmt.Println("cimgui experiment window closed") })
	b.Run(a.frame)
}

func (a *app) frame() {
	io := imgui.CurrentIO()
	display := io.DisplaySize()
	if display.X <= 1 || display.Y <= 1 {
		return
	}

	// The backend enables docking and platform viewports by default. This root
	// window opts out of docking and creates no extra platform windows; Stage 1
	// intentionally remains a single native window test.
	imgui.SetNextWindowPos(imgui.NewVec2(0, 0))
	imgui.SetNextWindowSize(display)
	flags := imgui.WindowFlagsNoDecoration | imgui.WindowFlagsNoMove |
		imgui.WindowFlagsNoSavedSettings | imgui.WindowFlagsNoDocking | imgui.WindowFlagsNoBackground
	if !imgui.BeginV("DDGO cimgui-go capability test", nil, flags) {
		imgui.End()
		return
	}

	available := imgui.ContentRegionAvail()
	controlsHeight := imgui.TextLineHeightWithSpacing()*4.0 + 16
	viewportSize := imgui.NewVec2(available.X, float32(math.Max(40, float64(available.Y-controlsHeight))))
	viewportStart := imgui.CursorScreenPos()
	buttonFlags := imgui.ButtonFlagsMouseButtonLeft | imgui.ButtonFlagsMouseButtonRight | imgui.ButtonFlagsMouseButtonMiddle
	imgui.InvisibleButtonV("##toolpath-viewport", viewportSize, buttonFlags)
	viewportMin, viewportMax := imgui.ItemRectMin(), imgui.ItemRectMax()
	a.hovered = imgui.IsItemHovered()
	a.active = imgui.IsItemActive()
	a.handleViewportInput(io)
	a.updateAnimation(io.DeltaTime())
	if a.markerDirty {
		a.renderer.updateMarker(a.tool)
		a.markerDirty = false
	}
	a.renderer.render(framebufferRect(viewportMin, viewportMax, io), a.camera)

	// This draw-list border is submitted by ImGui after the direct OpenGL scene,
	// so it verifies that the two renderers share the framebuffer cleanly.
	imgui.WindowDrawList().AddRectV(viewportStart, viewportMax, imgui.ColorConvertFloat4ToU32(imgui.NewVec4(0.42, 0.60, 0.78, 1)), 0, 1, imgui.DrawFlagsNone)

	imgui.Separator()
	if imgui.Button("Reset View") {
		a.camera.reset()
	}
	imgui.SameLine()
	label := "Animate"
	if a.animating {
		label = "Stop"
	}
	if imgui.Button(label) {
		a.animating = !a.animating
	}
	imgui.SameLine()
	imgui.TextUnformatted(fmt.Sprintf("Segments: %d   Current: %d   FPS: %.0f", len(a.path), int(a.progress)%len(a.path), a.fps))
	imgui.TextUnformatted(fmt.Sprintf("Viewport hover: %t  active: %t  drag: %t  mouse delta: %.1f, %.1f  wheel: %.1f",
		a.hovered, a.active, a.dragging, a.lastDelta.X, a.lastDelta.Y, a.lastWheel.Y))
	imgui.TextDisabled("Left drag: orbit   Right/middle drag: pan   Wheel: zoom")
	imgui.End()
}

func (a *app) handleViewportInput(io *imgui.IO) {
	delta := io.MouseDelta()
	wheel := io.MouseWheel()
	a.lastDelta = delta
	a.lastWheel = imgui.NewVec2(0, wheel)
	down := io.MouseDown()
	if !down[0] && !down[1] && !down[2] {
		a.dragging = false
	}
	if a.hovered && (down[0] || down[1] || down[2]) {
		a.dragging = true
	}
	if a.dragging {
		if down[0] {
			a.camera.orbit(delta.X, delta.Y)
		}
		if down[1] || down[2] {
			a.camera.pan(delta.X, delta.Y)
		}
	}
	// Wheel ownership is resolved from the item's hover state, not from global
	// GLFW polling. A wheel event over an ordinary ImGui control cannot zoom.
	if a.hovered && wheel != 0 {
		a.camera.zoom(wheel)
	}
}

func (a *app) updateAnimation(dt float32) {
	if dt > 0 {
		a.fps = a.fps*0.92 + 0.08/dt
	}
	if !a.animating {
		return
	}
	a.progress += dt * 100 // elapsed time, not assumed frame rate
	for a.progress >= float32(len(a.path)) {
		a.progress -= float32(len(a.path))
	}
	segment := a.path[int(a.progress)]
	a.tool = pointLerp(segment.Start, segment.End, a.progress-float32(int(a.progress)))
	a.markerDirty = true
}

func framebufferRect(min, max imgui.Vec2, io *imgui.IO) rect {
	scale := io.DisplayFramebufferScale()
	display := io.DisplaySize()
	x := int32(math.Round(float64(min.X * scale.X)))
	w := int32(math.Round(float64((max.X - min.X) * scale.X)))
	h := int32(math.Round(float64((max.Y - min.Y) * scale.Y)))
	fbHeight := int32(math.Round(float64(display.Y * scale.Y)))
	// ImGui's rectangle begins at top-left. The OpenGL viewport/scissor begins
	// at bottom-left, hence the subtraction of the logical item's lower edge.
	y := fbHeight - int32(math.Round(float64(max.Y*scale.Y)))
	return rect{x: x, y: y, w: w, h: h}
}
