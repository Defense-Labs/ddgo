# GoGPU CNC toolpath capability spike

This is an isolated Stage-1 experiment for the DDGO desktop UI question. It
creates one native GoGPU window with a `gogpu/ui` control area and an embedded
`core/gpuview.GPUView`. The viewport is rendered directly by a small custom
`github.com/gogpu/wgpu` renderer; it is not a browser, WebView, foreign
language renderer, g3d scene, or DDGO production package.

The renderer batches a deterministic synthetic CNC path (about 2,000 line
segments) into a static line-list vertex buffer. Cutting lines are orange and
rapids are blue. It also has static XYZ axes and an XY grid, a depth buffer,
and a small dynamic 3-axis tool marker. Only the camera uniform and marker
buffer are changed per frame; the path and grid buffers are created once.

## Toolchain and modules

The upstream UI documentation for the selected snapshot requires Go 1.25+ and
`CGO_ENABLED=0`. The host's default Go was `go1.23.1`; it was not replaced.
For the verified build/run, Go's managed `go1.25.0` toolchain was fetched into
the normal Go toolchain cache with `GOTOOLCHAIN=go1.25.0+auto`. No `/tmp/`
tooling, no system Go replacement, and no CGO were required.

Exact resolved direct GoGPU versions (`go.mod` / `go.sum`):

| Module | Version |
| --- | --- |
| `github.com/gogpu/ui` | `v0.1.54` |
| `github.com/gogpu/gg` | `v0.52.3` |
| `github.com/gogpu/gogpu` | `v0.53.0` |
| `github.com/gogpu/gpucontext` | `v0.28.0` |
| `github.com/gogpu/wgpu` | `v0.31.4` |
| `github.com/gogpu/gputypes` | `v0.5.2` |

Resolved support modules are `github.com/coregx/signals v0.1.1`,
`github.com/go-webgpu/goffi v0.6.3`, `github.com/go-webgpu/webgpu v0.5.5`,
`github.com/gogpu/naga v0.19.0`, `golang.org/x/image v0.45.0`,
`golang.org/x/sys v0.47.0`, and `golang.org/x/text v0.41.0`.

These are intentionally a compatible UI family. A first attempt to use the
then-newer standalone `wgpu v0.34.5` with `ui v0.1.54` failed because its
`gg v0.52.3` dependency still calls the older `wgpu` render-pass API. The
coherent module selection above resolves that upstream version skew.

## Build and run

From this directory:

```sh
GOTOOLCHAIN=go1.25.0+auto CGO_ENABLED=0 go build ./...
GOTOOLCHAIN=go1.25.0+auto CGO_ENABLED=0 GOGPU_GRAPHICS_API=vulkan go run .
```

`GOGPU_GRAPHICS_API=vulkan` was tested on this Linux host. The native window
launched and selected:

```text
AMD Radeon RX 9070 (RADV GFX1201), Vulkan, DiscreteGPU
```

The RADV driver emitted its normal “not a conformant Vulkan implementation,
testing use only” warning. No renderer initialization or per-frame error was
logged during the 12-second native launch. `GOGPU_GRAPHICS_API=gles` is a
reasonable diagnostic fallback command but was not needed or tested here;
software rendering was not used as evidence for this spike.

## Manual runtime result: input failure

The Vulkan run visibly rendered the toolpath, grid, axes, and marker in the
embedded GPUView, but manual testing found the native application completely
unresponsive to input. Neither the ordinary `Reset View` / `Animate / Stop`
buttons nor pointer and wheel input in the viewport produced an observable
action. Therefore orbit, pan, zoom, reset, and user-controlled animation are
**not verified and do not pass this Stage-1 experiment**.

No compiler error, application panic, or useful runtime input error was
logged. The observed failure is in the native event path between
`github.com/gogpu/gogpu`'s event source and the `github.com/gogpu/ui/app`
desktop/UI event bridge on this Linux/X11 test host and selected release
family. GPUView itself is primarily a rendering widget and does not offer a
public camera-input behavior.

Small application-level workarounds were attempted and deliberately not kept:

1. A local widget wrapper forwarding events to the embedded GPUView.
2. `gesture.DragRecognizer` / gesture hit testing in that wrapper.
3. Polling `gogpu.App.Input()` from the render callback.
4. A raw `PointerEventSource` / `ScrollEventSource` fanout which retained the
   UI callback while also sending the native events to the viewport.

None made the buttons or viewport responsive in manual tests. The failed
fanout code was removed so the checked-in experiment uses the normal GoGPU
event source and remains minimal; its behavior and rationale are recorded
here. No GoGPU source was modified. A DDGO implementation would need a
reliable supported platform input route (or an upstream GoGPU fix) before it
could meet the interactive-view requirement.

## Implemented rendering behavior

- The code contains camera orbit, pan, zoom, and reset operations, but the
  current runtime cannot invoke them because of the input failure above.
- The `Animate / Stop` callback advances the marker along the prebuilt segment
  list, but cannot be user-triggered on the tested host.
- The renderer uses `PrimitiveTopologyLineList`, WGSL, a `Depth32Float`
  attachment, depth comparison `Less`, and direct `wgpu` command encoding.
- GoGPU's documented `gpucontext` opaque-handle `Pointer()` conversion is used
  for the device, queue, and supplied offscreen texture view. No locally
  invented resource conversion is used.

The current `GPUView` itself intentionally consumes no input, so this
experiment includes a very small local compositor wrapper that delegates
layout and drawing to it. The wrapper forwards GPUView's `Texture()` and
`ViewportSize()` contract and is a repaint boundary: that is necessary for
ui's compositor to emit its external texture layer. On this host/release the
normal UI event bridge did not provide a usable interaction path. No GoGPU
source was modified.

`GPUView` does not publicly expose the supplied target's color format. For
the selected UI release, its public compositor implementation allocates the
offscreen target as `BGRA8Unorm`, so the line pipeline uses that format rather
than assuming the window surface format. This is a small application-level
coupling to document when evaluating the API.

## Resizing limitation

The upstream `GPUView` public API allocates its offscreen backing texture from
`gpuview.Size(width, height)` and exposes no resize/recreate operation. This
experiment configures a 960×600 target. Normal UI layout can constrain or
scale the displayed widget when the window changes size, but the backing target
and depth texture remain at 960×600, so this is **not clean dynamic viewport
resizing**. A production implementation would need a public GPUView resize API
or an application-level view recreation policy; neither was added to this
capability spike.

## Assessment

The experiment passes the rendering half of the capability check: an isolated
pure-Go module builds with `CGO_ENABLED=0`, launches one native Vulkan window,
composes visible UI around a GPUView, and shares the offscreen target with a
custom direct, depth-tested `wgpu` line renderer. It also demonstrates static
batched CNC geometry, visually distinct rapid/cutting moves, axes, grid, and a
dynamic marker without a browser, WebView, CGO GUI toolkit, or production DDGO
code.

It does **not** establish the minimum DDGO UI requirement on this host/release:
ordinary UI interaction and viewport orbit/pan/zoom/reset/animation are
nonfunctional in manual testing, and clean GPUView dynamic resizing is absent.
This is therefore a negative Stage-1 result rather than evidence that GoGPU is
currently suitable for the required interactive DDGO toolpath view.

The custom rendering portion is approximately 225 lines (pipeline, buffers,
WGSL, depth target, and drawing) plus about 100 lines of small local camera and
animation code. A viable DDGO path would additionally need to own or obtain a
reliable input integration and a viewport-resize policy.
