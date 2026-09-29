// gogpu-toolpath is an isolated Stage-1 capability experiment. It embeds a
// direct wgpu line-list renderer in gogpu/ui's GPUView widget.
package main

import (
	"fmt"
	"log"
	"time"

	_ "github.com/gogpu/gg/gpu" // enable the GPU text/compositor path used by ui/desktop
	"github.com/gogpu/gogpu"
	"github.com/gogpu/gpucontext"
	"github.com/gogpu/ui/app"
	"github.com/gogpu/ui/core/button"
	"github.com/gogpu/ui/core/gpuview"
	"github.com/gogpu/ui/desktop"
	"github.com/gogpu/ui/event"
	"github.com/gogpu/ui/geometry"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/theme/material3"
	"github.com/gogpu/ui/widget"
	"github.com/gogpu/wgpu"
)

func main() {
	path := makeToolpath()
	camera := newCamera()
	state := &animationState{path: path, tool: path[0].Start}

	gogpuApp := gogpu.NewApp(gogpu.DefaultConfig().
		WithTitle("GoGPU CNC Toolpath Capability Spike").
		WithSize(1024, 760))
	m3 := material3.New(widget.Hex(0x146C94))
	uiApp := app.New(
		app.WithWindowProvider(gogpuApp),
		app.WithPlatformProvider(gogpuApp),
		app.WithEventSource(gogpuApp.EventSource()),
		app.WithTheme(m3.AsTheme()),
	)

	var renderer *lineRenderer
	var rendererInitErr error
	view := gpuview.New(
		gpuview.Size(viewportWidth, viewportHeight),
		gpuview.Continuous(true), // intentionally simple for the Stage-1 animation experiment
		gpuview.OnRender(func(target gpucontext.TextureView) {
			if target.IsNil() || rendererInitErr != nil {
				return
			}
			if renderer == nil {
				provider := gogpuApp.GPUContextProvider()
				if provider == nil || provider.Device().IsNil() || provider.Queue().IsNil() {
					return // App.Run has not initialized its device yet.
				}
				// gpucontext deliberately exposes opaque handles. This is its supported
				// conversion mechanism; no application-defined unsafe layout is used.
				device := (*wgpu.Device)(provider.Device().Pointer())
				queue := (*wgpu.Queue)(provider.Queue().Pointer())
				// GPUView supplies a texture view but no public texture-format
				// accessor. ui v0.1.54 allocates these offscreen targets as
				// BGRA8Unorm, which need not equal the window surface format.
				log.Printf("GPUView target format=BGRA8Unorm (window surface format=%v)", provider.SurfaceFormat())
				renderer, rendererInitErr = newLineRenderer(device, queue, path)
				if rendererInitErr != nil {
					log.Printf("line renderer initialization: %v", rendererInitErr)
					return
				}
			}
			state.advance(time.Now())
			wgpuTarget := (*wgpu.TextureView)(target.Pointer())
			if err := renderer.Render(wgpuTarget, camera, state.tool); err != nil {
				log.Printf("line renderer frame: %v", err)
			}
		}),
	)
	interactive := newInteractiveView(view)

	uiApp.SetRoot(primitives.Box(
		primitives.Text("GoGPU direct wgpu CNC toolpath").FontSize(22).Bold().Color(widget.RGBA8(28, 35, 42, 255)),
		primitives.Text("Left-drag orbit • right/middle-drag pan • wheel zoom").FontSize(13).Color(widget.RGBA8(85, 95, 105, 255)),
		interactive,
		primitives.HBox(
			button.New(button.TextOpt("Reset View"), button.OnClick(func() { camera.Reset(); view.Invalidate() })),
			button.New(button.TextOpt("Animate / Stop"), button.OnClick(func() { state.running = !state.running; state.last = time.Now(); view.Invalidate() })),
			primitives.Text(fmt.Sprintf("Segments: %d", len(path))).FontSize(14).Color(widget.RGBA8(45, 55, 65, 255)),
		).Gap(12),
	).Padding(18).Gap(10).Background(widget.RGBA8(241, 244, 247, 255)))

	gogpuApp.OnClose(func() {
		view.Release()
		if renderer != nil {
			renderer.Release()
		}
	})
	if err := desktop.Run(gogpuApp, uiApp); err != nil {
		log.Fatal(err)
	}
}

type animationState struct {
	path     []Segment
	running  bool
	segment  int
	fraction float32
	tool     Point
	last     time.Time
}

func (a *animationState) advance(now time.Time) {
	if a.last.IsZero() {
		a.last = now
		return
	}
	dt := float32(now.Sub(a.last).Seconds())
	a.last = now
	if !a.running || len(a.path) == 0 {
		return
	}
	a.fraction += dt * 75 // segments per second; only the marker buffer changes.
	for a.fraction >= 1 {
		a.fraction--
		a.segment = (a.segment + 1) % len(a.path)
	}
	s := a.path[a.segment]
	a.tool = Point{X: s.Start.X + (s.End.X-s.Start.X)*a.fraction, Y: s.Start.Y + (s.End.Y-s.Start.Y)*a.fraction, Z: s.Start.Z + (s.End.Z-s.Start.Z)*a.fraction}
}

// interactiveView is a tiny local compositor wrapper. GPUView correctly owns
// the offscreen target but intentionally does not consume input.
type interactiveView struct {
	widget.WidgetBase
	view *gpuview.Widget
}

func newInteractiveView(view *gpuview.Widget) *interactiveView {
	w := &interactiveView{view: view}
	w.SetVisible(true)
	w.SetEnabled(true)
	// GPUView owns the texture, but this wrapper is the tree-visible widget.
	// Mirror its repaint-boundary/external-texture contract so ui's compositor
	// emits an ExternalTextureLayer for the texture after Draw initializes it.
	w.SetRepaintBoundary(true)
	return w
}
func (w *interactiveView) Layout(ctx widget.Context, c geometry.Constraints) geometry.Size {
	return w.view.Layout(ctx, c)
}
func (w *interactiveView) Draw(ctx widget.Context, canvas widget.Canvas) {
	w.view.SetBounds(w.Bounds())
	w.view.Draw(ctx, canvas)
}
func (w *interactiveView) Children() []widget.Widget { return nil }

// Texture and ViewportSize deliberately mirror GPUView's public contract.
// ui/app detects this structural interface and composites the supplied target.
func (w *interactiveView) Texture() gpucontext.TextureView { return w.view.Texture() }
func (w *interactiveView) ViewportSize() (int, int)        { return w.view.ViewportSize() }

func (w *interactiveView) Event(_ widget.Context, _ event.Event) bool { return false }
