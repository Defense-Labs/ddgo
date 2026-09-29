# Gio native 3D toolpath capability experiment

This is an isolated Stage-1 experiment for a single Gio-owned desktop window
containing a direct OpenGL ES 3D toolpath viewport and ordinary Gio Material
controls. It does not import DDGO code or share code with the other UI
experiments.

## Result

**Gio + `app.Window`: PASS on the recorded X11 host.**

The viewport receives a dedicated, clipped pointer target. Press, release,
move, drag, cancel, and wheel events are registered only above the toolbar;
the `Reset View` and `Animate`/`Stop` Material buttons therefore coexist with
camera input. A pointer grab is requested on press so drags continue outside
the initial viewport bounds. Left drag orbits, right/middle drag pans, and the
wheel zooms. Reset changes camera state only. Animation changes only the
small tool-marker VBO; toolpath and grid VBOs remain static.

The synthetic deterministic path contains about 2,100 segments with multiple
Z levels, plunges/retracts, dense XY cutting motion, circular rising features,
rapid moves, and a final retract. Cutting lines are green and rapid lines are
orange. The XY grid and XYZ axes use the same batched line pipeline. Rendering
uses real GLES depth testing (`GL_DEPTH_TEST`, `GL_LESS`).

## Recorded environment

- Host OS: Linux (X11 session, `DISPLAY=:1`)
- Go: 1.23.8 minimum (the installed `go1.23.1` bootstrap downloaded Go 1.23.8
  because Gio 0.9.0 requires it)
- Gio: `gioui.org` v0.9.0
- Rendering: direct OpenGL ES through CGO calls to EGL/GLESv2, EGL GLES 3
  context
- Recorded graphics API: OpenGL ES 3.2 Mesa 25.1.5
- Recorded GPU renderer: AMD Radeon RX 9070 (`radeonsi`, gfx1201, ACO)
- Math: small local camera/matrix code; no math dependency
- CGO: required, both by Gio's custom EGL renderer path and direct GLES calls

## Build and run

The primary path uses Gio's own `app.Window`, not GLFW. Current upstream
`gio-example/opengl` documents that its Linux custom-renderer `app.ViewEvent`
path requires the `nowayland` tag; this host is X11, so this is the command
that was built and run:

```sh
go build -tags nowayland ./...
go run -tags nowayland .
```

`go build ./...` also compiles the module when normal Gio Linux dependencies
are available, but use `-tags nowayland` for the tested native-window route.
On a Wayland session this selects Gio's X11/XWayland implementation. That is
an explicit compatibility limitation, not an invisible implementation detail;
native Wayland custom-renderer support remains a comparison concern.

Required Linux development packages are provided through `pkg-config`:

```text
EGL, GLESv2, wayland-egl, X11, xkbcommon and Gio's usual Linux dependencies
```

No system packages were changed and no absolute `/tmp` path is embedded in
the source. Temporary Go module/build caches may be needed in restricted
environments.

## Architecture and lifecycle cost

`main.go` adapts the current upstream `gioui/gio-example/opengl` custom
renderer lifecycle (SPDX `Unlicense OR MIT` is retained in adapted files): it
locks the OS thread, receives `app.ViewEvent`, creates an EGL native-window
surface, makes the GLES context current, shares it with `gpu.New`, responds to
view replacement/resizing, renders direct GLES first, renders Gio operations
into the shared context, swaps EGL buffers, then acknowledges the frame.

The renderer itself is small: one shader program; one static path VBO; one
static grid/axis VBO; and one dynamic marker VBO. The surrounding EGL/native
view lifecycle is materially more boilerplate than the renderer itself
(roughly half of `main.go`), and is the primary Gio-specific comparison cost
against Qt and Wails.

## Verification notes

The Phase-1 input-only window was run before the scene existed. Its visible
input counter increased when the pointer was used over the viewport; this
proved that Gio public pointer routing worked before the renderer was added.
The completed application was then run in the same native X11 window. Resize
updates the EGL surface where required, the GLES viewport, projection aspect,
toolbar placement, and clipped input region. No GLFW fallback was used.
