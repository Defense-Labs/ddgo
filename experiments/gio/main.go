// SPDX-License-Identifier: Unlicense OR MIT

//go:build linux

// gio-toolpath is an isolated Gio app.Window + OpenGL ES capability experiment.
// Its EGL/app.ViewEvent lifecycle is adapted from gioui/gio-example/opengl.
package main

import (
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/gpu"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

/*
#cgo linux pkg-config: egl wayland-egl
#cgo CFLAGS: -DEGL_NO_X11
#cgo LDFLAGS: -lEGL -lGLESv2

#include <EGL/egl.h>
#include <GLES3/gl3.h>
#define EGL_EGLEXT_PROTOTYPES
#include <EGL/eglext.h>
*/
import "C"

const toolbarDP = 62

type eglContext struct {
	view    C.EGLNativeWindowType
	disp    C.EGLDisplay
	ctx     C.EGLContext
	surf    C.EGLSurface
	cleanup func()
}

type viewportInput struct {
	// A dedicated Gio event target: never use the window itself for viewport input.
	tag      struct{}
	events   int
	last     string
	lastPos  pointer.Event
	dragging bool
	lastDrag bool
	lastBtns pointer.Buttons
}

func main() {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("Gio CNC Toolpath Capability Spike"))
		w.Option(app.Size(unit.Dp(900), unit.Dp(700)))
		// Gio's official custom OpenGL example requires this for app.Window.
		w.Option(app.CustomRenderer(true))
		if err := loop(w); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}()
	app.Main()
}

func discardContext(w *app.Window, gioGPU gpu.GPU, eglCtx *eglContext, renderer *lineRenderer) error {
	var err error
	w.Run(func() {
		if gioGPU != nil {
			gioGPU.Release()
		}
		if eglCtx != nil {
			if ok := C.eglMakeCurrent(eglCtx.disp, eglCtx.surf, eglCtx.surf, eglCtx.ctx); ok != C.EGL_TRUE {
				err = fmt.Errorf("eglMakeCurrent failed (%#x)", C.eglGetError())
			} else {
				if renderer != nil {
					renderer.Release()
				}
				eglCtx.Release()
			}
		}
	})
	return err
}

func recreateContext(w *app.Window, ve app.ViewEvent, gioGPU gpu.GPU, eglCtx *eglContext, size image.Point) (gpu.GPU, *eglContext, error) {
	if err := discardContext(w, gioGPU, eglCtx, nil); err != nil {
		return nil, nil, err
	}
	var err error
	w.Run(func() { eglCtx, err = createContext(ve, size) })
	if err != nil {
		return nil, nil, fmt.Errorf("create EGL context: %w", err)
	}
	if ok := C.eglMakeCurrent(eglCtx.disp, eglCtx.surf, eglCtx.surf, eglCtx.ctx); ok != C.EGL_TRUE {
		return nil, eglCtx, fmt.Errorf("eglMakeCurrent failed (%#x)", C.eglGetError())
	}
	str := func(e C.GLenum) string { return C.GoString((*C.char)(unsafe.Pointer(C.glGetString(e)))) }
	log.Printf("GL_VERSION=%s; GL_RENDERER=%s", str(C.GL_VERSION), str(C.GL_RENDERER))
	gioGPU, err = gpu.New(gpu.OpenGL{ES: true, Shared: true})
	return gioGPU, eglCtx, err
}

func loop(w *app.Window) (windowErr error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	var ops op.Ops
	var reset, animate widget.Clickable
	var input viewportInput
	path := makeToolpath()
	camera := newCamera()
	animation := animationState{tool: path[0].Start}
	var renderer *lineRenderer
	var ctx *eglContext
	var gioCtx gpu.GPU
	var view app.ViewEvent
	var size image.Point

	for {
		switch e := w.Event().(type) {
		case app.ViewEvent:
			view = e
			windowErr = errors.Join(windowErr, discardContext(w, gioCtx, ctx, renderer))
			gioCtx, ctx = nil, nil
			renderer = nil
		case app.DestroyEvent:
			return errors.Join(windowErr, e.Err)
		case app.FrameEvent:
			resized := e.Size != size
			size = e.Size
			if view.Valid() && size != (image.Point{}) && gioCtx == nil {
				var err error
				gioCtx, ctx, err = recreateContext(w, view, gioCtx, ctx, size)
				windowErr = errors.Join(windowErr, err)
			} else if view.Valid() && resized && ctx != nil {
				ctx.resizeSurface(view, size)
			}
			if windowErr != nil {
				w.Perform(system.ActionClose)
				continue
			}

			gtx := app.NewContext(&ops, e)
			bar := gtx.Dp(unit.Dp(toolbarDP))
			viewport := image.Rect(0, 0, e.Size.X, max(0, e.Size.Y-bar))
			registerViewportInput(gtx, viewport, &input, camera)
			// Input is deliberately registered before and clipped separately from controls.
			for reset.Clicked(gtx) {
				camera.Reset()
				input.last = "Reset View button clicked"
			}
			for animate.Clicked(gtx) {
				animation.running = !animation.running
				animation.last = e.Now
			}
			animation.advance(e.Now, path)
			if animation.running {
				gtx.Execute(op.InvalidateCmd{})
			}
			layoutControls(gtx, th, &reset, &animate, &input, len(path), animation)

			C.eglWaitClient()
			if renderer == nil {
				var err error
				renderer, err = newLineRenderer(path)
				if err != nil {
					windowErr = errors.Join(windowErr, err)
					continue
				}
			}
			viewportHeight := max(1, e.Size.Y-bar)
			renderer.Draw(e.Size.X, viewportHeight, camera.Matrix(float32(e.Size.X)/float32(viewportHeight)), animation.tool)
			if err := gioCtx.Frame(gtx.Ops, gpu.OpenGLRenderTarget{}, e.Size); err != nil {
				windowErr = errors.Join(windowErr, fmt.Errorf("Gio frame: %w", err))
				continue
			}
			if ok := C.eglSwapBuffers(ctx.disp, ctx.surf); ok != C.EGL_TRUE {
				windowErr = errors.Join(windowErr, fmt.Errorf("eglSwapBuffers: 0x%x", C.eglGetError()))
				continue
			}
			e.Frame(gtx.Ops)
		}
	}
}

func registerViewportInput(gtx layout.Context, viewport image.Rectangle, s *viewportInput, camera *Camera) {
	stack := clip.Rect(viewport).Push(gtx.Ops)
	event.Op(gtx.Ops, &s.tag)
	stack.Pop()
	for {
		e, ok := gtx.Event(pointer.Filter{Target: &s.tag, Kinds: pointer.Press | pointer.Release | pointer.Move | pointer.Drag | pointer.Scroll | pointer.Cancel})
		if !ok {
			return
		}
		p, ok := e.(pointer.Event)
		if !ok {
			continue
		}
		previous := s.lastPos
		s.events++
		s.lastPos = p
		s.lastBtns = p.Buttons
		s.last = fmt.Sprintf("%s  x=%.0f y=%.0f", p.Kind, p.Position.X, p.Position.Y)
		switch p.Kind {
		case pointer.Press:
			s.dragging, s.lastDrag = true, true
			gtx.Source.Execute(pointer.GrabCmd{Tag: &s.tag, ID: p.PointerID})
		case pointer.Drag:
			if s.lastDrag {
				dx, dy := p.Position.X-previous.Position.X, p.Position.Y-previous.Position.Y
				if p.Buttons.Contain(pointer.ButtonPrimary) {
					camera.Orbit(dx, dy)
				} else if p.Buttons.Contain(pointer.ButtonSecondary) || p.Buttons.Contain(pointer.ButtonTertiary) {
					camera.Pan(dx, dy)
				}
			}
			s.lastDrag = true
		case pointer.Scroll:
			camera.Zoom(p.Scroll.Y)
		case pointer.Release, pointer.Cancel:
			s.dragging, s.lastDrag = false, false
		}
	}
}

func layoutControls(gtx layout.Context, th *material.Theme, reset, animate *widget.Clickable, in *viewportInput, segments int, animation animationState) layout.Dimensions {
	bar := gtx.Dp(unit.Dp(toolbarDP))
	stack := op.Offset(image.Pt(0, gtx.Constraints.Max.Y-bar)).Push(gtx.Ops)
	defer stack.Pop()
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, bar))
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return layout.Spacer{Width: unit.Dp(12)}.Layout(gtx) }),
		layout.Rigid(material.Button(th, reset, "Reset View").Layout),
		layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
		layout.Rigid(material.Button(th, animate, animation.label()).Layout),
		layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
		layout.Rigid(material.Body1(th, fmt.Sprintf("Segments: %d  Segment: %d", segments, animation.segment)).Layout),
		layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
		layout.Rigid(material.Body2(th, fmt.Sprintf("Input: %d  Last: %s", in.events, in.last)).Layout),
	)
}

type animationState struct {
	running  bool
	segment  int
	fraction float32
	tool     Point
	last     time.Time
}

func (a *animationState) advance(now time.Time, path []Segment) {
	if a.last.IsZero() {
		a.last = now
		return
	}
	dt := float32(now.Sub(a.last).Seconds())
	a.last = now
	if !a.running || len(path) == 0 {
		return
	}
	a.fraction += dt * 85
	for a.fraction >= 1 {
		a.fraction--
		a.segment = (a.segment + 1) % len(path)
	}
	a.tool = lerpSegment(path[a.segment], a.fraction)
}
func (a animationState) label() string {
	if a.running {
		return "Stop"
	}
	return "Animate"
}

func createContext(ve app.ViewEvent, size image.Point) (*eglContext, error) {
	view, cleanup := nativeViewFor(ve, size)
	if view == C.EGLNativeWindowType(0) {
		return nil, errors.New("no native window")
	}
	disp := getDisplay(ve)
	if disp == C.EGLDisplay(0) {
		return nil, fmt.Errorf("eglGetDisplay: 0x%x", C.eglGetError())
	}
	var major, minor C.EGLint
	if C.eglInitialize(disp, &major, &minor) != C.EGL_TRUE {
		return nil, fmt.Errorf("eglInitialize: 0x%x", C.eglGetError())
	}
	extensions := strings.Split(C.GoString(C.eglQueryString(disp, C.EGL_EXTENSIONS)), " ")
	srgb := hasExtension(extensions, "EGL_KHR_gl_colorspace")
	attrs := []C.EGLint{C.EGL_RENDERABLE_TYPE, C.EGL_OPENGL_ES2_BIT, C.EGL_SURFACE_TYPE, C.EGL_WINDOW_BIT, C.EGL_BLUE_SIZE, 8, C.EGL_GREEN_SIZE, 8, C.EGL_RED_SIZE, 8, C.EGL_DEPTH_SIZE, 24}
	if srgb {
		attrs = append(attrs, C.EGL_ALPHA_SIZE, 8)
	}
	attrs = append(attrs, C.EGL_NONE)
	var cfg C.EGLConfig
	var n C.EGLint
	if C.eglChooseConfig(disp, &attrs[0], &cfg, 1, &n) != C.EGL_TRUE || n == 0 {
		return nil, fmt.Errorf("eglChooseConfig: 0x%x", C.eglGetError())
	}
	ctxAttrs := []C.EGLint{C.EGL_CONTEXT_CLIENT_VERSION, 3, C.EGL_NONE}
	ctx := C.eglCreateContext(disp, cfg, nil, &ctxAttrs[0])
	if ctx == nil {
		return nil, fmt.Errorf("eglCreateContext: 0x%x", C.eglGetError())
	}
	surfAttrs := []C.EGLint{C.EGL_NONE}
	if srgb {
		surfAttrs = []C.EGLint{C.EGL_GL_COLORSPACE, C.EGL_GL_COLORSPACE_SRGB, C.EGL_NONE}
	}
	surf := C.eglCreateWindowSurface(disp, cfg, view, &surfAttrs[0])
	if surf == nil {
		C.eglDestroyContext(disp, ctx)
		return nil, fmt.Errorf("eglCreateWindowSurface: 0x%x", C.eglGetError())
	}
	return &eglContext{view: view, disp: disp, ctx: ctx, surf: surf, cleanup: cleanup}, nil
}

func (c *eglContext) resizeSurface(ve app.ViewEvent, size image.Point) {
	nativeViewResize(ve, c.view, size)
}
func (c *eglContext) Release() {
	if c.ctx != nil {
		C.eglDestroyContext(c.disp, c.ctx)
	}
	if c.surf != nil {
		C.eglDestroySurface(c.disp, c.surf)
	}
	if c.cleanup != nil {
		c.cleanup()
	}
	*c = eglContext{}
}
func hasExtension(exts []string, want string) bool {
	for _, e := range exts {
		if e == want {
			return true
		}
	}
	return false
}
