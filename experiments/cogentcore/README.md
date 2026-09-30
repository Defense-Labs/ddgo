# Cogent Core UI / 3D capability experiment

An isolated Stage-1 DDGO capability experiment. It has a separate Go module,
does not import DDGO production packages, and intentionally duplicates its
synthetic path generator.

## Result: LIMITED

The required native architecture worked on the test host: ordinary Cogent Core
widgets and an embedded `xyzcore.Scene` occupied one native window, rendering
the batched, depth-tested `xyz.Scene` using Cogent Core's WebGPU stack. The
Reset View and Animate/Stop buttons are normal Core controls outside the 3D
widget. The marker moves by changing only its `xyz.Solid` pose; static path
meshes are created once and are not reconstructed on animation frames.

This is **LIMITED**, rather than PASS, because the resolved version's default
camera navigation was observed to be extremely sensitive and minimally
polished for this CNC viewport. It was nonetheless functional in the live
test. This spike intentionally preserves Cogent Core's supplied navigation
rather than adding a custom controller, because that is the capability being
evaluated.

```text
UI: Cogent Core
3D widget: xyzcore.Scene
3D scene: xyz.Scene
Renderer: Cogent Core WebGPU / xyz
Toolpath representation: batched indexed triangle meshes
```

## Application structure

```text
Cogent Core window
├── xyzcore.Scene
│   └── xyz.Scene
│       ├── one cutting GenMesh / Solid
│       ├── one rapid GenMesh / Solid
│       ├── small grid and three axis solids
│       └── one sphere tool-position solid
└── normal Core controls: Reset View, Animate / Stop, status
```

`xyzcore.NewScene` is used directly; `SceneEditor` is not used. The scene has
ambient and directional light, opaque Phong materials, and a perspective
camera. Its depth texture is `gpu.Depth32`, as created by the normal
`xyz.Scene` render-frame path.

### Coordinates and geometry

CNC coordinates are mapped as follows:

```text
CNC X -> world X
CNC Y -> world Z
CNC Z -> world Y
```

Cogent Core uses world +Y as the camera-up direction, so positive CNC Z is
physically upward. Every toolpath segment is a thin, flat-shaded rectangular
prism made from 24 duplicated face vertices and 36 indexed triangle entries.
The custom `xyz.GenMesh` builder selects a stable perpendicular reference
axis for each segment, supplies face normals, and supplies zero UVs required
by the mesh interface.

The deterministic generated path contains **1,310 segments**: 1,298 cutting
and 12 rapid moves. It includes four raster cutting depths, plunges/retracts,
four circular features, rapid relocations, a variable-Z spiral, and final
retract. The primary path is exactly two `xyz.Solid` nodes:

| Mesh | Segments | Vertices | Indices |
| --- | ---: | ---: | ---: |
| cutting | 1,298 | 31,152 | 46,728 |
| rapid | 12 | 288 | 432 |
| total primary path | 1,310 | 31,440 | 47,160 |

The small grid has 34 prism segments; it and the three axes are reference
geometry only. Static geometry is allocated and registered before the window
is shown. During playback the code only changes the marker pose and requests a
normal `xyzcore.Scene` render.

## Interaction and animation

The resolved `xyz.Scene` documentation/code describes its default navigation
as drag orbit, **Shift + drag** pan, and scroll zoom (Alt + drag pans target).
No custom camera controller is present. Live validation established that the
default 3D navigation, normal controls, scene embedding, and resize path all
work in one window. The observed default drag sensitivity is the material
usability concern behind the classification above; it should be reassessed
before relying on it for a production CNC view.

`Reset View` restores an explicit `ddgo-default` saved camera. `Animate` uses
`view.Animate`; in Core v0.3.42 `core.Animation.Dt` is milliseconds, which the
experiment converts to seconds before advancing at 55 work units/second. The
camera remains managed by the scene while the animation only moves the marker.

## Build and run

From this directory:

```sh
go test ./...
go build .
./cogentcore
```

The test verifies deterministic segment count and that the two primary meshes
have exactly 24 vertices and 36 indices per segment. The live application was
launched successfully after `go build .`, outside `go run` and without a
source-tree runtime path.

## Resolved software and observed host

Tested 2026-09-29 on:

| Item | Observed value |
| --- | --- |
| Cogent Core | `cogentcore.org/core v0.3.42` (latest tagged version queried from the Go module proxy) |
| Module/toolchain requirement | `go 1.25.6` |
| Go initially on PATH | `go1.23.1 linux/amd64` |
| Go actually used | automatic downloaded `go1.25.6 linux/amd64` |
| OS | Pop!_OS 22.04 LTS, Linux 6.17.9-76061709-generic, amd64 |
| Display server | X11, `DISPLAY=:1`, `XDG_SESSION_TYPE=x11` |
| GPU / driver | AMD Radeon RX 9070, Mesa RADV 25.1.5 (`GFX1201`) |
| Vulkan | Vulkan 1.3 instance; GPU reports Vulkan API 1.4.311 |
| WebGPU backend | Vulkan is evidenced by the live RADV warning; no adapter/backend-name log is exposed by this application |
| CGO | enabled (`CGO_ENABLED=1`) |

The live run printed `WARNING: radv is not a conformant Vulkan implementation,
testing use only.` It nevertheless created the native window and rendered the
scene. This is a Mesa/RADV warning, not an application crash.

The produced Linux binary is dynamically linked only to `libc`, `libm`, and
`libgcc_s` according to `ldd`; it did not show a system `libvulkan` or `wgpu`
entry. That does **not** eliminate runtime graphics requirements: Cogent Core's
WebGPU dependency provides native WebGPU support and the observed execution
still required the host X11 display, Vulkan loader/driver, and a working GPU
driver. The source dependency graph also includes GLFW integration and native
WebGPU Linux support. A C compiler and the platform C/GLFW/X11 development
dependencies are relevant for fresh Linux builds because CGO is enabled.

No system package was installed or upgraded. The only compatibility action was
Go's normal automatic download of Go 1.25.6, with build cache placed at
`/tmp/ddgo-cogentcore-go-cache`; no `/tmp` path is committed or required at
runtime. The current Go 1.25.6 baseline is a deployment/build concern for
DDGO, as are X11/Wayland support and a compatible Vulkan driver. This run did
not force X11: it used the host's existing X11 session. Wayland remains
untested.

## API note and future picking

Cogent Core's tree planner gives `tree.AddChild` a call-site-generated plan
name. Repeated children made from one loop need `tree.AddChildAt` with explicit
unique names; the three axis solids use that public API. This is a small API
workaround, not a renderer patch.

`xyzcore` selection is primarily organized around `xyz.Node` / `xyz.Solid`.
The main toolpath intentionally has only two solids, so future segment-level
picking will likely require custom ray-versus-batched-mesh logic (or a
separate acceleration/indexing layer). Picking is deliberately not implemented
in this Stage-1 experiment.

## Performance observations

Startup was subjectively prompt after Go/module compilation. The live window
rendered the 1,310-segment / 31,440-vertex primary path, normal controls, and
animation marker. Orbit/pan/zoom and resize were tested interactively; no
static-mesh rebuild or obvious resize stall was observed at this data size.
No formal timing or memory benchmark was performed. The default camera feel,
not the batched mesh size, was the visible usability limitation.
