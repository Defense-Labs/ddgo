# GTK4 / gotk4 GtkGLArea capability experiment

This isolated DDGO capability experiment has no dependency on DDGO production code or the other UI experiments.

> **Final classification: inappropriate.** This experiment was cancelled before
> completing Stage 1. The current gotk4-generated GTK stack requires a much
> newer native GTK/GLib development and runtime environment than the host
> provides. A temporary compatible runtime proved materially too large and
> operationally burdensome for this capability spike. This is a scope/deployment
> decision, not a GTK or gotk4 functional failure.

```text
UI: GTK4 native widgets
3D viewport: GtkGLArea
3D renderer: direct OpenGL
Go binding: gotk4 / CGO
```

The program creates one `GtkApplicationWindow`, with an expanding `GtkGLArea` above normal GTK `Reset View` and `Animate`/`Stop` buttons plus a status label. It has a deterministic Go-generated 3,000+ segment path, static cutting/rapid VBOs, static grid/axis VBO, and a small dynamic marker VBO. There is no G-code parsing, DDGO import, GLFW/SDL window, or custom GTK C bridge.

## Binding APIs used

The resolved module is `github.com/diamondburned/gotk4/pkg v0.4.1` (commit `f90c213bbae79f6dcb74c5101a88b630f158de25`, 2026-08-08, generated GTK branch `4`). Its pinned `nixpkgs-gotk4` input is GTK **4.22.4** (`21ea275a7c46aef9d4d6ddc962e6d562e9d94183`). The root `github.com/diamondburned/gotk4` module is now the generator; its separately versioned `pkg` module contains the generated bindings used here.

The resolved generated APIs were inspected before implementation. This uses:

- `gtk.NewGLArea`, `SetHasDepthBuffer`, `SetAllowedApis(gdk.GLAPIGL)`, `SetRequiredVersion`, `MakeCurrent`, `Error`, `QueueRender`, `ConnectRender`, and `ConnectResize`.
- `Widget.ConnectRealize` and `Widget.ConnectUnrealize` for GL lifetime.
- `NewEventControllerMotion`, `NewEventControllerScroll(EventControllerScrollVertical)`, and `NewGestureDrag` with `GestureSingle.SetButton`.

`SetAllowedApis` exists in this gotk4 binding and requests desktop GL. The shader is GLSL 1.50 and requests an OpenGL 3.2 context; it does not use GLES or create a second context.

## Controls and rendering design

- Left drag orbits, right drag pans, and vertical wheel/touchpad scroll zooms.
- Reset View changes only camera state and queues a draw.
- Animate/Stop uses a GTK/GLib 16 ms timeout and elapsed time. No Go goroutine calls GTK or OpenGL.

The render callback draws to GTK's already-current framebuffer and never binds framebuffer zero. Realization makes the GLArea current, checks `GLArea.Error`, initializes `go-gl`, and creates shader/VAO/VBO resources. Unrealization makes it current and deletes all GL objects. Static geometry uploads once; only the marker buffer changes per render. Depth uses `GL_DEPTH_TEST` and `GL_LESS`.

On realization the executable logs selected GLArea API, actual depth bits, `GL_VERSION`, `GL_VENDOR`, `GL_RENDERER`, and GLSL version. A successful host run should record those values instead of guessing them.

## Build and run

From this directory:

```sh
CGO_ENABLED=1 go build ./...
./gtk4
```

Gotk4/pkg v0.4.1 requires Go 1.24+. The local Go 1.23.1 used its automatic Go 1.26.8 toolchain while resolving this module. `CGO_ENABLED=1` is intentional.

Linux development dependencies (package names vary):

```text
gtk4 development headers and pkg-config file       (for example libgtk-4-dev)
gobject-introspection development files            (for example libgirepository1.0-dev)
graphene development files                         (for example libgraphene-1.0-dev)
OpenGL / EGL / GLX development files               (for example libgl1-mesa-dev, libegl1-mesa-dev)
pkg-config, C compiler, and GTK's transitive Pango/GDK-Pixbuf/GLib dependencies
```

The relevant pkg-config modules include `gtk4`, `glib-2.0`, `gobject-introspection-1.0`, `graphene-gobject-1.0`, `graphene-1.0`, and the GL/EGL stack selected by the GTK backend. `go-gl` uses the system OpenGL loader; `go-gl/glfw` is not imported.

## Host record (2026-09-29)

| Item | Observed value |
| --- | --- |
| Host OS | Ubuntu 22.04-derived Linux, amd64 |
| Go initially on PATH | go1.23.1 linux/amd64 |
| C compiler | GCC 11.4.0 |
| CGO_ENABLED used | 1 |
| GTK runtime | libgtk-4-1 4.6.9+ds-0ubuntu0.22.04.2 |
| GTK development/header version | absent (`gtk4.pc` and GTK4 headers absent) |
| Display session | X11 (`DISPLAY=:1`, `XDG_SESSION_TYPE=x11`) |
| OpenGL runtime libraries | `libGL.so.1`, `libEGL.so.1` present |
| go-gl | `github.com/go-gl/gl` v0.0.0-20260331235117-4566fea9a276 (`v3.2-core/gl`) |
| math | `github.com/go-gl/mathgl` v1.2.0 |

The native build was deliberately attempted with a temporary writable Go cache:

```text
GOCACHE=/tmp/ddgo-gtk4-go-build-cache CGO_ENABLED=1 go build ./...
```

It stopped before compiling this application because the host is missing `gobject-introspection-1.0`, `graphene-gobject-1.0`, and `graphene-1.0` pkg-config development modules (and also lacks `gtk4.pc`). Therefore this host did **not** start the Go executable, create a GL context, or produce GL vendor/renderer/GLSL values.

This is a **host-system development/version limitation**, not a GtkGLArea runtime failure. The GTK runtime is 4.6.9; the current gotk4 generated package is built from a GTK 4.22.4 API set and contains newer APIs such as `GtkGLArea.SetAllowedApis`. Installing only the Ubuntu 22.04 GTK4 development package would not be a valid test of this current binding stack.

A matching disposable GTK 4.22.4/GLib 2.88.1/Graphene/GObject-Introspection environment was subsequently obtained below `/tmp` to assess that compatibility route. It required approximately 3.2 GB after extracting the Nix runtime and development closures. The experiment was cancelled before completing a build or launch in that namespace, because this burden was judged inappropriate for the intended isolated Stage-1 comparison. All temporary packages, caches, compatibility prefixes, and Nix store data were removed; no system package was installed or upgraded and no `/tmp` path is committed.

Classification of evidence:

- **GTK limitation:** none observed; no GtkGLArea context was created here.
- **gotk4 binding limitation:** none in the required surface. The required APIs exist and need no custom C wrapper. The friction is the separately versioned generated `pkg` module and Go 1.24+ requirement.
- **host-system limitation:** current GTK4 is 4.6.9 and GTK4, Graphene, and GObject-introspection development packages are absent.
- **experiment decision:** inappropriate. The required temporary current GTK stack was too large and complex for this spike, so runtime capability is intentionally uncharacterized.

The Stage-1 application is intentionally **not classified as passed or failed**. It was cancelled as inappropriate before verifying window/GLArea coexistence, depth, pointer and gesture delivery, resize, animation responsiveness, and GL strings.

## Deployment implications

This will be a CGO-linked Go binary, not a self-contained GTK application. It can run without the source tree but needs GTK shared libraries plus GTK runtime data: GSettings schemas, module/loaders as applicable, icon/theme data, and the system OpenGL/EGL/driver stack. Executable size was not measured because a native binary cannot link on this host.

- Linux: GTK4/runtime data normally come from the distribution or an app-specific bundled runtime.
- Windows: a GTK runtime DLL/data bundle and OpenGL driver/runtime are required; untested.
- macOS: a GTK runtime/framework-style bundle and GL dependencies are required; untested.

The direct OpenGL portion is roughly 300 of the 564 Go lines: shader compilation, three static line batches, one marker VBO, matrices, and lifecycle cleanup. Packaging and installers are out of scope.
