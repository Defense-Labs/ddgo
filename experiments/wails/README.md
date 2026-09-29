# DDGO Wails v2 3D capability spike

This isolated experiment tests whether a single Wails desktop window can combine ordinary HTML controls with an embedded interactive Three.js CNC toolpath view. It does not import DDGO packages, parse G-code, or participate in normal DDGO builds and tests.

```text
Go App.GetToolpath() --generated Wails binding--> vanilla TypeScript
                                                 |--> two static Three.js LineSegments
                                                 '--> local marker playback
```

`App.GetToolpath` deterministically produces 1,816 segments in Go: three pocket depths, plunges/retracts, rapid moves, two approximated circular profiles, wavy variable-Z finishing passes, and a final retract. The frontend requests it exactly once using Wails-generated `frontend/wailsjs/go/main/App` bindings. It builds cutting and rapid `BufferGeometry` objects once; the render loop only updates the marker and controls.

This is a **WebView / web-technology UI architecture**, not a native-widget UI architecture. On Linux Wails hosts it in WebKitGTK; buttons and layout are HTML/CSS, and Three.js is rendered through the WebView's WebGL implementation.

## UI behavior

- Cyan cutting geometry and orange rapid geometry are batched separately.
- `OrbitControls` provides orbit, pan, and zoom.
- **Reset View** restores the saved camera position and target without rebuilding the scene.
- **Animate** / **Stop** moves a sphere through the cached segment list using `requestAnimationFrame`; no Go call or static geometry rebuild occurs per frame.
- A `ResizeObserver` updates renderer dimensions and camera projection.
- CNC coordinates map to Three.js as `(X, Z, -Y)`, keeping CNC Z vertical.

## Resolved versions and test host

| Item | Value |
| --- | --- |
| Wails | `github.com/wailsapp/wails/v2` **v2.16.0** |
| Go module directive | Go 1.25.0 |
| Go used by Wails build | Go 1.26.8 automatic toolchain (host initially had Go 1.23.1) |
| Node / npm | Node v19.9.0 / npm 9.6.3 |
| Three.js | 0.186.1 |
| Type declarations | `@types/three` 0.186.0 (development-only) |
| Vite | 6.2.0 |
| OS | Pop!_OS 22.04, Linux amd64 |
| GPU reported by `wails doctor` | AMD Radeon RX 9070/9070 XT/9070 GRE, `amdgpu` |

Three.js is the only rendering dependency. Vite 7, selected by the original template range, requires Node 20.19+ and failed on this host's Node 19 (`crypto.hash is not a function`) in `wails dev`. Vite was pinned to 6.2.0 for this experiment; production and development runs succeeded on the host. Use a supported current Node LTS for a fresh setup rather than relying on Node 19.

## Install and run

From this directory, after the dependencies are already available:

```bash
cd frontend
npm install
cd ..

wails dev
wails build
```

The Linux host used for this experiment needs the supported newer WebKitGTK 4.1 ABI tag, so its actual commands were:

```bash
export TMP_WEBKIT_ROOT="/tmp/ddgo-wails-webkit"
export PKG_CONFIG_PATH="$TMP_WEBKIT_ROOT/pkgconfig:$TMP_WEBKIT_ROOT/root/usr/lib/x86_64-linux-gnu/pkgconfig"
export CGO_LDFLAGS="-L$TMP_WEBKIT_ROOT/root/usr/lib/x86_64-linux-gnu"

wails dev -tags webkit2_41
wails build -tags webkit2_41
```

When the Wails CLI is not installed, the equivalent tested invocation is:

```bash
go run github.com/wailsapp/wails/v2/cmd/wails@v2.16.0 build -tags webkit2_41
```

The production binary is [build/bin/ddgo-wails-ui3d](build/bin/ddgo-wails-ui3d).

## Linux WebKitGTK finding and temporary workaround

`wails doctor` reported Wails v2.16.0, GCC, GTK 3, npm, and pkg-config as installed, but reported `libwebkit2gtk-4.0-dev` as **available, not installed**. The host did have both WebKitGTK 4.0 and 4.1 runtime libraries at version 2.50.4, but neither development headers nor pkg-config files.

No system packages were changed. For this test only, matching packages were downloaded and extracted below `$TMP_WEBKIT_ROOT`: `libwebkit2gtk-4.1-dev`, `libjavascriptcoregtk-4.1-dev`, `libsoup-3.0-dev`, `libsysprof-4-dev`, and their direct development metadata dependencies, plus matching 4.1 WebKit, JavaScriptCore, and libsoup runtime packages. Small temporary pkg-config files pointed at those extracted headers and libraries. Wails v2.16.0's documented `webkit2_41` source path then built successfully. This location is not part of the repository or binary and is not required on a machine with normal WebKitGTK 4.1 development packages installed.

The temporary dependency setup is a development-machine limitation, not an observed Wails limitation. A normal Linux development host should install its distribution's WebKitGTK development package; exact package names and the appropriate Wails tag depend on the ABI supplied by that distribution.

## Validation and limitations

Both `wails dev -tags webkit2_41` and `wails build -tags webkit2_41` completed. The built application was launched on this host and successfully showed the interactive Three.js WebGL viewport in the Wails WebView: the Go-provided path, axes/grid, distinct rapid and cutting moves, tool marker, controls, camera interaction, animation, and resize behavior worked.

WebGL creation therefore worked in the actual Wails/WebKitGTK application, not an external browser. Mesa emitted repeated warnings that its bundled LLVM did not recognize the new AMD `gfx1201` target; rendering and interaction still worked. This matches a host graphics-stack warning rather than a Wails or Three.js failure. The test did not capture a WebGL renderer-string diagnostic, so it establishes usable WebGL on this AMD/Mesa host but does not make a more specific hardware-renderer claim.

The output JavaScript bundle is roughly 543 kB before gzip, largely Three.js. This is a capability spike only; it deliberately omits DDGO integration, G-code/file loading, stock simulation, picking, persistence, and performance benchmarking.
