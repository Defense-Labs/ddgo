# DDGO Go + Qt 6 3D capability spike

This isolated application tests one specific stack:

```text
Go -> MIQT Qt 6 bindings -> QQmlApplicationEngine -> Qt Quick 3D View3D
```

It does not import DDGO packages or participate in the repository's normal
build. Go deterministically generates 3,205 CNC-style segments, encodes them as
JSON, and installs that JSON as the `toolpathJSON` QML context property. QML
parses it once and builds two static `ProceduralMesh` line geometries: one for
cutting moves and one for rapids.

The scene also contains an XY grid, X/Y/Z axes, an animated tool marker, and an
`OrbitCameraController`. Drag to orbit, Ctrl+drag to pan, and use the mouse wheel
to zoom. The toolbar contains ordinary Qt Quick Controls for resetting the view
and starting or stopping traversal.

## Versions and requirements

- Go: developed with Go 1.23.1; `go.mod` requires Go 1.23.
- MIQT: `github.com/mappu/miqt` v0.13.0, pinned by `go.mod` and `go.sum`.
- Qt: **Qt 6.6 or newer is required**. `ProceduralMesh` was introduced in Qt
  6.6. MIQT v0.13.0 also describes its Qt 6 bindings as targeting Qt 6.4+.
- CGO: required (`CGO_ENABLED=1`).
- Native toolchain: `pkg-config`, a C++17-capable compiler, and the Qt 6
  development files visible through pkg-config.
- Qt modules: Qt Base, Qt Declarative/QML, Qt Quick, Qt Quick Controls, Qt Quick
  Layouts, Qt Quick 3D, Qt Quick 3D Helpers, and Qt Shader Tools. The Quick 3D
  QML plugin links against Shader Tools at runtime.

On a Debian-family distribution new enough to provide Qt 6.6+, the package set
is conceptually:

```text
build-essential
pkg-config
qt6-base-dev
qt6-declarative-dev
qt6-quick3d-dev
qt6-shadertools-dev
qml6-module-qtquick
qml6-module-qtquick-controls
qml6-module-qtquick-layouts
qml6-module-qtquick-window
qml6-module-quick3d
qml6-module-quick3d-helpers
```

Package names differ by distribution. The included `Dockerfile` uses Fedora 41,
whose Qt 6.8 packages are named `qt6-qtbase-devel`,
`qt6-qtdeclarative-devel`, `qt6-qtquick3d-devel`, and
`qt6-qtshadertools-devel`.

## Build and run

From this directory, with a suitable native Qt installation:

```bash
CGO_ENABLED=1 go build -o ddgo-go-qt-demo .
./ddgo-go-qt-demo
```

MIQT obtains compiler and linker flags from pkg-config. For a Qt installation
outside the system prefix, point pkg-config and the dynamic loader at it:

```bash
export PATH="/path/to/Qt/6.8.0/gcc_64/bin:$PATH"
export PKG_CONFIG_PATH="/path/to/Qt/6.8.0/gcc_64/lib/pkgconfig"
export LD_LIBRARY_PATH="/path/to/Qt/6.8.0/gcc_64/lib:${LD_LIBRARY_PATH:-}"
CGO_ENABLED=1 go build -o ddgo-go-qt-demo .
./ddgo-go-qt-demo
```

The container recipe can verify compilation in an isolated Qt 6.8 environment:

```bash
docker build -t ddgo-go-qt-demo .
```

Running the GUI from that container requires forwarding the host's display and
graphics devices, so native execution is the intended interactive test.

For the development-machine validation, Qt 6.8.3 was installed outside the
system prefix with `aqtinstall`:

```bash
aqt install-qt linux desktop 6.8.3 linux_gcc_64 \
  --modules qtquick3d qtshadertools \
  -O /tmp/ddgo-qt

export QT_ROOT=/tmp/ddgo-qt/6.8.3/gcc_64
export PATH="$QT_ROOT/bin:$PATH"
export PKG_CONFIG_PATH="$QT_ROOT/lib/pkgconfig"
export LD_LIBRARY_PATH="$QT_ROOT/lib:${LD_LIBRARY_PATH:-}"
export QML_IMPORT_PATH="$QT_ROOT/qml"
CGO_ENABLED=1 go build -o ddgo-go-qt-demo .
./ddgo-go-qt-demo
```

## Development-machine findings

The development host used for this spike has:

- Go 1.23.1 on Linux/amd64;
- Qt Base 6.2.4 from Ubuntu/Pop!_OS Jammy packages;
- no Qt Declarative or Qt Quick 3D development/runtime packages;
- no Qt 6 pkg-config metadata (`Qt6Widgets.pc`, `Qt6Qml.pc`, and related files).

Consequently, a build using only the host's system Qt stops during MIQT's
pkg-config step:

```text
Package Qt6Widgets was not found in the pkg-config search path.
No package 'Qt6Widgets' found
```

Installing Jammy's available Declarative and Quick 3D packages would still
provide Qt 6.2.4, which cannot load `ProceduralMesh`. A coherent temporary Qt
6.8.3 installation was therefore used for the successful build and run instead
of mixing the system's 6.2 Base libraries with newer modules.

The interactive validation used Qt's OpenGL RHI backend. Qt created an OpenGL
4.6 compatibility-profile context through Mesa on an AMD Radeon RX 9070. Qt
Quick 3D selected this backend automatically. For diagnostic testing it can be
forced explicitly with:

```bash
QSG_RHI_BACKEND=opengl ./ddgo-go-qt-demo
```

The validated application opened one native window and rendered all 3,205
Go-generated segments. Cutting and rapid motion were visibly distinct; the XY
grid, axes, depth changes, and tool marker were visible. Orbit, pan, zoom,
Reset View, Animate/Stop, and live resizing all worked while the animation was
running.

Mesa printed shader-compiler warnings that its bundled LLVM did not recognize
the GPU target name `gfx1201`, but the OpenGL context, scene rendering, and all
interactions continued to work. This appears to be a host graphics-stack
diagnostic rather than an MIQT or QML failure.

## Limitations and conclusions

- The JSON/context-property transfer is deliberately simple and copies the
  path into QML. It is suitable for this 3,205-segment capability test, not a
  production geometry-transfer design.
- Line width and line rendering quality depend on the selected RHI backend.
- `ProceduralMesh` keeps the implementation to two path models instead of one
  scene object per segment, but it raises the minimum Qt version to 6.6.
- No `QOpenGLWidget` fallback was implemented. A native bridge could expose a
  custom `QQuick3DGeometry` or an OpenGL widget plus vertex upload and camera
  input, but that would add C++ ownership and rendering code before the direct
  Qt Quick 3D route has been tested with the required Qt version.

Qt Quick 3D has licensing considerations separate from Qt Base. Those terms,
along with Qt and MIQT licensing obligations, must be reviewed before any
production adoption. This experiment does not make that licensing decision.
