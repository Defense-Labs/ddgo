# Fyne 2.8 viewport capability experiment

This independent Fyne application answers a narrow DDGO question: can current Fyne provide one desktop window with ordinary Fyne controls and a scalable, interactive 3D CNC toolpath viewport through supported public APIs? It imports no DDGO code and has no dependency on any other experiment.

## Result: FAIL

Fyne 2.8.1 supports the desktop UI and custom viewport input, and it can draw a fragment shader in a canvas rectangle. It does **not** expose an application-facing arbitrary 3D geometry/render-pass API. In particular, the public API has no application-provided vertex buffer or vertex shader, line-list topology, depth buffer/state, arbitrary GL draw call, or current GL-context callback. A DDGO `[]Segment` therefore cannot be submitted as thousands of camera-transformed, depth-tested GPU line segments through supported Fyne APIs.

That missing public capability is the reason for `FAIL`, not a failure to open a window. Solving it would require Fyne internals/private driver state, a fork, a separate native GL window, or building a CPU/software renderer around Fyne; none is an acceptable Stage-1 implementation.

## What this program demonstrates

`Viewport` embeds `widget.BaseWidget` and implements only public interaction interfaces needed here: `desktop.Mouseable`, `desktop.Hoverable`, `fyne.Draggable`, and `fyne.Scrollable`. The live overlay records mouse press and release counts, drag deltas, wheel value, and hover position. Left drag updates orbit-like yaw/pitch values, right/middle drag updates pan, and wheel updates zoom. `Reset View` resets this state. The viewport is separate from the toolbar, so Fyne buttons do not manipulate viewport state.

The deterministic Go generator makes **2,935** `Segment` values: XY cutting passes at four Z depths, plunges, rapid repositions, a rising circular/helical feature, and a final retract. It is deliberately local to this experiment and does not parse G-code.

The background is a small `canvas.Shader` investigation. It uses `Source` and `SourceES` fragment shader source plus `yaw`, `pitch`, and `zoom` scalar uniforms to draw a fixed procedural cube. `Animate`/`Stop` uses `canvas.NewShaderAnimation`; its label explicitly says that this is shader-only diagnostic animation, **not** toolpath traversal. This confirms that the shader fits and resizes as a normal canvas object and can react to scalar camera-like uniforms. It is not evidence of arbitrary toolpath support.

`canvas.Shader` accepts only:

- desktop and GLES fragment-shader source;
- named scalar `float32` uniforms; and
- named `image.Image` texture inputs.

It is painted as Fyne's rectangle primitive. It has no public path from the Go `[]Segment` to arbitrary GPU vertices/lines. Encoding the 2,935 segments in a texture and scanning it per output pixel would be a pixels-times-segments fragment-shader algorithm, so it was intentionally not attempted.

For a bounded CPU-projection diagnostic, the widget creates only the first 48 `canvas.Line` objects. It rotates and projects those 48 lines on camera changes; it has no clipping, depth sorting, or attempt to scale to the complete path. Extending that diagnostic to the data set would require 2,934 independent Fyne objects and updating them all for each view change (plus manual depth handling). That is an architectural limitation, not a renderer to optimize. No raster or software-rendering diagnostic was attempted.

## Public API investigation

The inspected Fyne 2.8.1 public API consists of custom `fyne.CanvasObject`s, custom-widget renderers that return ordinary canvas objects, and 2D canvas primitives. `canvas.Shader` is the only relevant new GPU-facing public object, and its documented contract is the rectangle fragment shader described above. The Fyne source confirms that it supplies Fyne's fixed rectangle vertices and issues a fixed triangle-strip draw internally; this experiment neither imports nor calls that internal package. No Fyne internal package, fork, `go:linkname`, native GL handle, or separate rendering window is used.

## Resize, input, and threading

The custom renderer's `Layout` resizes the shader, repositions the overlay, and reprojects the small CPU diagnostic using the new widget size. Input coordinates are Fyne-relative to the viewport. All state changes occur inside Fyne callbacks or the Fyne shader animation mechanism; no worker goroutine is used, so `fyne.Do` is not required here. A real background worker would have to marshal UI changes through `fyne.Do`.

## Recorded environment

- Host OS: Pop!_OS 22.04 LTS, Linux 6.17.9-76061709-generic, x86_64
- Go used: go1.23.1 (`go 1.23.0` module directive)
- Fyne resolved: `fyne.io/fyne/v2 v2.8.1` (stable 2.8 release line)
- Graphics backend/driver: Fyne desktop's GLFW/OpenGL path; host `glxinfo -B` reported AMD Radeon RX 9070 (`radeonsi`, gfx1201, ACO), Mesa 25.1.5, OpenGL 4.6 core / GLES 3.2.
- CGO/native dependencies: required on Linux by Fyne's GLFW/OpenGL desktop backend. Install the usual distribution X11/Wayland, OpenGL, and GLFW build dependencies when they are not already present.

## Build and run

From this directory:

```sh
go build ./...
go run .
```

The run opens one Fyne desktop window. Exercise the mouse over the large viewport, then click `Reset View` and `Animate`/`Stop`; the status label and input overlay distinguish these interactions. The application was kept as a useful, small input and shader diagnostic despite the final `FAIL` result.

The application was launched on the recorded X11 display as a viewable 980×720 window. Injected live mouse input produced this viewport diagnostic sequence: enter, primary press, drag `(38, 19)`, release, drag end, wheel, secondary press, and secondary release. This verifies Fyne delivery of the required viewport input interfaces; the ordinary toolbar buttons remain separate Fyne widgets and do not route through that viewport.

## Code size and maintenance implication

The experiment contains a small local path generator, one custom widget, minimal CPU projection for 48 lines, and two short fragment shader variants. It intentionally stops before adding a z-buffer, clipping pipeline, thousands of `canvas.Line` updates, or a software rasterizer. Those additions would be application-owned rendering infrastructure rather than a credible Fyne 3D viewport foundation.
