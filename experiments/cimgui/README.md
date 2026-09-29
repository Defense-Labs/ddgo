# cimgui-go UI / 3D capability experiment

An isolated Stage-1 test of cimgui-go for an interactive CNC toolpath and
ordinary UI controls in one desktop window. It has no DDGO production imports
and intentionally does not share implementation code with the other spikes.

## Result

**Passed on this host, using a local rebuild of cimgui-go's native `cimgui.a`
for glibc compatibility.** The live test opened one 1000x750 GLFW window,
rendered the static 3D path directly into an ImGui-defined subregion, and
showed ordinary ImGui controls below it. Left drag orbited, right drag panned,
the wheel zoomed, Reset View worked, and Animate changed to Stop while moving
the small tool marker. A later 900x620 live resize kept the OpenGL viewport and
UI layout aligned.

The current GLFW backend enables Dear ImGui docking and platform viewports in
`CreateWindow`. On this host that put the root ImGui layout in a detached
upper-left platform pane, while the GLFW window contained the OpenGL scene.
That violates this spike's one-native-window scope. The application explicitly
disables `DockingEnable` and `ViewportsEnable` immediately after window
creation. No docking or multi-viewport behavior is exercised here.

## Architecture

```text
UI model: Dear ImGui immediate mode
window/input: GLFW (owned by cimgui-go GLFW backend)
3D: direct OpenGL into the main framebuffer
Go binding: cimgui-go over C/C++
```

`main.go` follows the current `cimgui-go/examples/glfw` pattern: it locks the
main goroutine to its OS thread, creates `backend.CreateBackend(
glfwbackend.NewGLFWBackend())`, and uses context lifetime hooks. It does not
initialize GLFW separately and does not import `github.com/go-gl/glfw`.

The backend owns the GLFW window, OpenGL context, ImGui GLFW input backend,
ImGui OpenGL 3 renderer, main loop, and buffer swap. Application GL objects are
created in the after-context hook and destroyed in the before-destroy hook.

Each frame reserves the viewport with `InvisibleButtonV`, records its screen
rectangle and item hover/active state, converts that rectangle to framebuffer
pixels, and draws into only that OpenGL viewport/scissor. The Y conversion is
documented beside `framebufferRect`: ImGui has a top-left logical origin;
OpenGL has a bottom-left framebuffer-pixel origin. `DisplayFramebufferScale`
accounts for high DPI. A transparent root ImGui window leaves the direct scene
visible until cimgui-go renders the ImGui draw data after the callback.

Camera ownership comes from the item, rather than global GLFW polling:

- Left drag over the viewport: orbit.
- Right or middle drag over the viewport: pan.
- Wheel while the viewport is hovered: zoom.

The status line shows hover, active, drag, mouse delta, and wheel values. It
made Phase 1 interaction visible and prevented controls from changing camera
state.

## Scene and renderer

The deterministic generator creates 2,397 `Segment` values: three depth passes
over six circular/helical-looking pockets, plunges/retracts, rapid
repositioning, a straight XY facing pass, and a final retract. It is uploaded
once as one colored `GL_LINES` VBO; cutting is green and rapid is orange. Grid
and XYZ axes are another static line VBO. Only the six-line yellow tool marker
uses a dynamic VBO during animation.

The renderer uses one GLSL 1.30 line shader, VAOs/VBOs, depth testing
(`GL_LESS`), and scissor/viewport setup. It does not use an FBO, textures,
lighting, G-code parsing, or one draw call per segment. The custom scene and
ImGui renderer coexisted cleanly in the live test. The four application source
files total approximately 558 lines.

## Versions and host tested

| Item | Value |
| --- | --- |
| Go module | Go 1.24.0 |
| Toolchain used | Go 1.24.0 (cimgui-go v1.6.0 requires Go 1.24+) |
| cimgui-go | `github.com/AllenDang/cimgui-go v1.6.0` |
| OpenGL binding | `github.com/go-gl/gl/v3.3-core/gl` from `github.com/go-gl/gl v0.0.0-20260331235117-4566fea9a276` |
| Matrix math | `github.com/go-gl/mathgl/mgl32 v1.2.0` |
| OS | Pop!_OS 22.04 LTS, Linux 6.17.9, glibc 2.35 |
| Display system | X11, `DISPLAY=:1`; `glfwbackend.ForceX11()` was not required |
| OpenGL context | Mesa 25.1.5, OpenGL 4.6 Compatibility Profile |
| GL renderer | AMD Radeon RX 9070 (`radeonsi`, `gfx1201`, ACO) |
| Default framebuffer depth | 24 bits, queried from its depth attachment |

Mesa printed a non-fatal warning that LLVM did not recognize `gfx1201`; the
window nevertheless rendered and interacted correctly.

## Build and run

From this directory:

```sh
go build ./...
go run .
```

CGO is required. The GLFW backend links cimgui-go's bundled static `cimgui.a`
and `libglfw3.a`; Linux also needs host `dl`, OpenGL, and X11 libraries. A C
compiler and a C++ compiler are required for CGO and any local native rebuild.
The test host used GCC/G++ 11.4; the result dynamically used `libGL`, `libX11`,
`libstdc++`, and `libm`.

The backend creates a GL 3.0 / GLSL 1.30-compatible Linux context. This code
only requires that baseline, despite the tested machine exposing GL 4.6.

## Host glibc compatibility workaround

The normal v1.6.0 module build on this glibc 2.35 host failed at final link:

```text
undefined reference to `__isoc23_sscanf`
```

The upstream prebuilt `cimgui.a` was built on a newer system and requires that
symbol. This is the older-Linux compatibility issue documented by cimgui-go,
not a Go, GLFW, or renderer-code problem.

For validation only, cimgui-go was copied to `/tmp/ddgo-cimgui-go-v1.6.0` and
its `lib/CMakeLists.txt` was configured with CMake 3.22.1. `cimgui.a` was
rebuilt there with host GCC/G++ 11.4, replacing only that temporary copy's
`lib/linux/x64/cimgui.a`. A temporary module `replace` allowed `go build ./...`
and the actual window launch. The absolute `replace` has been removed from this
experiment's `go.mod`; no `/tmp` path or local native archive is committed.

Deployment concern: target distributions must be new enough for upstream's
prebuilt native archives, or packaging must rebuild/pin native cimgui-go
libraries for the target environment.

## Capability observations

- One desktop GLFW window: yes, after disabling platform viewports/docking for
  this single-window stage.
- Direct OpenGL and normal ImGui controls in one framebuffer: yes.
- Hover/active state, drag delta, and wheel state: yes, displayed and live-tested.
- Orbit, pan, zoom, Reset View, animation, and responsive controls: yes.
- Static path rebuild during animation: no; only the marker VBO changes.
- Resize/input alignment: yes, in a live 900x620 resize test.
- X11 forcing: not required; normal backend operation was X11.
- Offscreen FBO fallback: not needed.

The major production-comparison concerns are the CGO/C++/native-library
deployment surface, prebuilt-archive glibc compatibility, and making an
explicit policy for docking/platform viewports instead of inheriting backend
defaults.
