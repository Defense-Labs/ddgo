# Releasing desktop artifacts

This project ships GUI desktop artifacts built with `-tags 'miqt serial'`.

## Release policy

- Minimum supported Windows: Windows 10, 64-bit
- Minimum supported macOS: `15.0`
- Linux release artifact: `DDGo-linux-amd64.tar.gz`
- macOS release artifact: `DDGo-macos-universal.dmg`
- macOS architectures: `arm64` + `x86_64`
- Windows release artifacts:
  - `DDGo-windows-amd64-setup.exe` (recommended installer)
  - `DDGo-windows-amd64.zip` (portable/debug package)

macOS 14 and earlier are no longer supported.

## Artifacts

- `DDGo-linux-amd64.tar.gz`
- `DDGo-macos-universal.dmg`
- `DDGo-windows-amd64-setup.exe`
- `DDGo-windows-amd64.zip`

## GitHub Actions runner policy

Use explicit macOS runner labels for release builds:

- Apple Silicon slice: `macos-15`
- Intel slice: `macos-15-intel`

Do not use `macos-14`, `macos-14-large`, `macos-latest`, or `macos-latest-large` for release artifacts.

## Linux

The Linux CI/CD artifact is `DDGo-linux-amd64.tar.gz`. It contains a full staged `DDGo-linux-amd64/` distribution:

```text
DDGo-linux-amd64/
  ddgo
  mockgrbl
  run-ddgo.sh
  lib/
  plugins/
    platforms/
      libqxcb.so
  README-artifact.txt
```

Users should extract the archive and run DDGo through the launcher:

```bash
./run-ddgo.sh
```

The launcher sets `LD_LIBRARY_PATH`, `QT_PLUGIN_PATH`, and `QT_QPA_PLATFORM_PLUGIN_PATH` so the MIQT/Qt GUI can find the bundled runtime libraries and Qt plugins. Running `./ddgo` directly may fail on systems that do not already provide matching Qt libraries/plugins.

`mockgrbl` is included in the same folder and can be run directly from the artifact for local development/testing.

The artifact bundles Qt runtime/plugin dependencies needed by the MIQT GUI, including the `xcb` platform plugin and related plugin directories copied from the build runner. The artifact is built on `ubuntu-latest` and bundles Qt/runtime libraries needed by the app. Compatibility with older distributions is limited by the runner's glibc/system ABI.

The workflow builds and verifies the archive with:

```bash
rm -rf dist
mkdir -p dist/build-linux

GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
  go build -tags 'miqt serial' -trimpath -ldflags='-s -w' \
  -o dist/build-linux/ddgo \
  ./cmd/ddgo

GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
  go build -trimpath -ldflags='-s -w' \
  -o dist/build-linux/mockgrbl \
  ./cmd/mockgrbl

bash scripts/package-linux-app.sh \
  dist/build-linux/ddgo \
  dist/build-linux/mockgrbl \
  dist/DDGo-linux-amd64

bash scripts/verify-linux-dist.sh dist/DDGo-linux-amd64
tar -C dist -czf dist/DDGo-linux-amd64.tar.gz DDGo-linux-amd64
```

## macOS universal build flow

The universal macOS app is built by packaging separate `arm64` and `x86_64` app bundles, then merging every Mach-O file with `lipo`. It is not sufficient to merge only `Contents/MacOS/ddgo`.

Both slices use the same pinned Qt distribution: Qt `6.12.0` desktop
`clang_64`, downloaded from Qt's distribution infrastructure with
`aqtinstall==3.3.0`. CD installs it below the runner temporary directory with:

```bash
aqt install-qt -O "$RUNNER_TEMP/qt" mac desktop 6.12.0 clang_64
```

The workflow locates the resulting `qmake` rather than assuming the final
architecture-directory name, verifies the queried Qt version, required
frameworks (`QtCore`, `QtGui`, and `QtWidgets`), `macdeployqt`, and the slice
architecture. Homebrew supplies only `pkgconf` as a build utility; Homebrew Qt
headers, frameworks, plugins, and pkg-config metadata are not used.

MIQT v0.15.0 discovers Qt through `pkg-config`. The
`scripts/configure-macos-qt.sh` helper first uses suitable Qt-provided
`Qt6Core`, `Qt6Gui`, and `Qt6Widgets` metadata when available. Official macOS
Qt packages do not guarantee those files, so the helper otherwise generates
minimal metadata from `qmake`'s installed paths and version, including only the
base framework modules MIQT requires. `PKG_CONFIG_LIBDIR` is set to that one
validated directory, preventing accidental resolution of a Homebrew Qt.

The Qt and installer versions are intentionally centralized in the macOS slice
job environment in `.github/workflows/cd.yml`. Change them together, then run
the macOS CD job: do not update either slice independently and do not add a
Homebrew Qt fallback. The Go build-cache key includes the pinned distribution,
installer version, deployment target, SDK build, compiler identity, and Go
dependency/configuration hashes. It intentionally does not include changing
application source files: Go's build cache already validates source and build
inputs within a compatible cache, while including every `*.go` file in an
immutable Actions cache key would force an unnecessary cold CGO build for each
ordinary edit. Packaging and verification scripts are likewise excluded unless
they configure Qt compilation; Qt itself is deliberately not cached until the
pinned installation path has proven reliable.

1. Build `dist/macos-arm64/DDGo.app` on `macos-15`.
2. Build `dist/macos-amd64/DDGo.app` on `macos-15-intel`.
3. Check pinned Qt metadata, required Mach-O file sets, architecture slices,
   framework install names, loaded-library inventories, and runtime search paths with
   `scripts/verify-macos-slice-compatibility.sh`.
4. Merge them with `scripts/merge-macos-universal.sh` into `dist/macos-universal/DDGo.app`.
5. Verify universality, macOS `15.0` deployment targets, bundled dependency
   paths, and codesigning with
   `scripts/verify-macos-universal-app.sh dist/macos-universal/DDGo.app 15.0`.
6. Build the final DMG with `scripts/create-macos-dmg.sh dist/macos-universal/DDGo.app dist/DDGo-macos-universal.dmg`.

`package-macos-app.sh` requires the validated `QT_ROOT` environment, records
the pinned Qt toolchain metadata in the bundle, sets `CFBundleExecutable=ddgo`
and `LSMinimumSystemVersion`, bundles Qt frameworks/plugins with that
distribution's `macdeployqt`, and signs the app. The pre-merge and final
verification scripts deliberately reject mismatched dependency inventories,
missing Mach-O slices, external Homebrew load paths, unresolved non-system
dependencies, or signing failures; matching version labels alone are not
sufficient.

Final-app verification statically resolves each required non-system Mach-O load
command using the referencing binary's `@loader_path`, `@executable_path`, and
the applicable direct `LC_RPATH` entries from that binary and the main
executable. The resolved target must be a real file inside the bundle; dangling
framework symlinks also fail verification. Legitimate system libraries remain
permitted, and unresolved weak-linked dependencies are reported as warnings.
This is intentionally not a complete dyld emulator: it does not prove
runtime-only `dlopen` calls or every loader-chain search case.
The normal `macdeployqt` layout is covered, including framework version
directories and symlinks. The universal merger explicitly extracts arm64 from
the Apple Silicon input and x86_64 from the Intel input, so universal Qt
framework inputs are supported; `x86_64h` is not accepted as a substitute for
the required `x86_64` slice.

`scripts/test-macos-toolchain.sh` provides platform-independent regression
coverage for cache-key composition and representative bundled dependency-path
resolution. It complements, but does not replace, the real macOS CD build,
packaging, dependency, signing, merge, and DMG verification.

## Windows (MSYS2 / MinGW)

DDGo Windows releases target Windows 10 or later on 64-bit/x64-compatible systems. Windows 7 and Windows 8/8.1 are unsupported. The installer enforces this support floor with Inno Setup `MinVersion=10.0`, and Windows release artifacts are amd64/x64-compatible. The portable ZIP has the same operating-system requirement; extracting it on an older Windows version does not make that version supported.

1. Install packages in an MSYS2 MINGW64 shell:
   `pacman -S --needed mingw-w64-x86_64-go mingw-w64-x86_64-gcc mingw-w64-x86_64-qt6-base mingw-w64-x86_64-pkgconf zip`
2. Build executable in that shell:
   `GOOS=windows GOARCH=amd64 CGO_ENABLED=1 go build -tags 'miqt serial' -ldflags '-s -w -H windowsgui' -o dist/ddgo-windows-amd64.exe ./cmd/ddgo`
3. Stage Windows app with runtime DLLs:
   `scripts/stage-windows-msys2.sh dist/ddgo-windows-amd64.exe dist/windows/DDGo`
4. Build portable zip from the staged folder:
   `(cd dist/windows && zip -r ../DDGo-windows-amd64.zip DDGo)`
5. Build installer from the same staged folder:
   `iscc /DSourceDir="...\dist\windows\DDGo" /DOutputDir="...\dist" installer\windows\DDGo.iss`
6. Verify distribution layout (staged, zip extract, and installer output):
   `pwsh -File scripts/verify-windows-dist.ps1 -DistDir <path>`

The installer and ZIP are both built from the same staged `dist/windows/DDGo` folder. Do not duplicate Qt/MinGW runtime-copying logic in the Inno Setup script.

The zip contains a `DDGo/` folder. Testers should extract the whole zip and run `DDGo/DDGo.exe`; moving the exe out of that folder will break Qt DLL/plugin loading.

Future: sign `DDGo.exe` and `DDGo-windows-amd64-setup.exe` with `signtool` before release upload.
