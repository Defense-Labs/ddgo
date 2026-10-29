# DDGo

DDGo is a Go-based GRBL-style CNC controller/operator UI. The repository is organized around a testable core controller, with optional real serial and Qt UI layers enabled through build tags.

## Current capabilities

- Serial transport abstraction with a fake transport for controller and package tests.
- Optional real USB/TTY serial transport behind the `serial` build tag.
- Optional MIQT/Qt6 Widgets UI behind the `miqt` build tag.
- Port discovery seam through `ports.ListFunc`, with real port listing behind the `serial` build tag.
- Console send path for manual controller commands.
- Jog and machine action commands, including jog cancel, unlock, home, hold, resume, status, and soft reset helpers.
- Status polling and status parsing for machine state, machine position, work position, feed, and spindle values.
- Independent G-code document windows with document-specific Run, Pause, Resume, Stop, progress, and retained execution history.
- Multi-document ownership and lifecycle management, including safe document close and shutdown coordination.
- Native read-only `.dd` package loading, guided job/step browsing, and bounded legacy-manifest compatibility handling.
- Supplementary package G-code windows with source display, execution controls, progress, and navigation back to their owning package.
- Manual execution of an individual G-code-containing package step from either its package window or supplementary view; both views share the same execution session.
- Program execution with pause, resume, stop, progress tracking, and terminal-response handling.
- Macro interception framework for registered application-level macro handlers from both loaded G-code programs and the interactive command prompt, with default built-in handlers for M100 midpoint write/verify, M101 WCS comparison, M102 expression write, M106 assertions, M107 variable store, M108 variable writeback, M109 contour point collection, and contour lifecycle control.
- Macro runtime query support for collecting query responses during an active program run.
- Macro runtime probe execution and last successful probe point capture.
- Contour point collection and lifecycle control through default macro handlers.
- WCS offset read/write helper support using `$#` and `G10 L2`.
- Process-local variable store and contour state primitives.
- Contour mode lifecycle reset on program start and program failure.
- Local MockGRBL controller for serial-style integration tests and controller development.

## What is not implemented yet

- The custom macro set is intentionally non-contiguous: M103-M105 are undefined and pass through like other unregistered commands, while M110 is unsupported legacy functionality rejected locally from files and the command prompt. Contour surface fitting, contour motion rewriting, and Z compensation are not active project requirements unless separately reintroduced later.
- The 3D toolpath panel is currently a placeholder.
- Full guided `.dd` package execution is not implemented. Conditional transitions, automatic pause/reset, timeout handling, and popup metadata are retained for compatibility but are not automatically executed. Manually running one step is not equivalent to running a package workflow.
- Machine profile/configuration is still future work.
- Persistent user settings are still future work.

Loading or browsing a package does not establish that its contents are safe or
authorized to run on a physical machine.

## Repository layout

- `internal/app`: controller orchestration, state, events, connection control, program runs, and runtime hooks.
- `internal/gcode`: G-code file loading and runnable-line parsing with raw and sanitized text.
- `internal/ddpackage`: read-only `.dd` ZIP/resource loader, manifest parser, compatibility handling, and package metadata model.
- `internal/grbl`: GRBL command construction and status parsing helpers.
- `internal/macro`: macro interception framework, runtime interfaces, WCS helpers, variables, and contour state primitives.
- `internal/transport`: serial transport interface, fake transport, and real/stub serial implementations.
- `internal/ports`: serial port discovery seam and real/stub implementations.
- `internal/frontend`: document registry/lifecycle coordination, execution sessions, package auxiliary-view ownership, and toolkit-independent presentation logic; `internal/frontend/miqt` contains the optional Qt6 implementation.
- `cmd/ddgo`: application entrypoint.
- `cmd/mockgrbl`: local GrblDD-style mock controller with PTY serial emulation and debug HTTP API.
- [`docs/architecture.md`](docs/architecture.md): current architecture notes for contributors.
- [`docs/dd-package-format.md`](docs/dd-package-format.md): `.dd` archive format, compatibility behavior, limits, and manual package-step execution boundaries.
- [`docs/macros.md`](docs/macros.md): macro framework status, runtime capabilities, limitations, and planned order.
- [`docs/mockgrbl.md`](docs/mockgrbl.md): mock controller usage, debug API, test hooks, and integration-test notes.
- [`docs/linux-serial.md`](docs/linux-serial.md): persistent Linux permissions and controller enumeration diagnostics.

## Build tags

The real serial implementation and MIQT UI are behind build tags:

- `serial` enables the real USB/TTY serial implementation and port discovery.
- `miqt` enables the Qt6 / MIQT UI.

This keeps the core logic testable on machines that do not have Qt installed.

## Testing

Core and stub-path tests work without Qt installed:

```bash
go test ./...
```

If you want to include the serial-tagged transport and port-listing tests on a machine with the serial dependency downloaded:

```bash
go test -tags serial ./internal/transport ./internal/ports ./internal/app ./internal/grbl
```

Linux mockgrbl integration tests use PTYs and require both `serial` and `mockgrbl_e2e` tags:

```bash
go test -tags 'serial mockgrbl_e2e' ./internal/integration/mockgrbl
```

For mock controller usage and debug endpoints, see [`docs/mockgrbl.md`](docs/mockgrbl.md).

## Build/run

The core/stub build does not require Qt:

```bash
go build ./...
```

The production GUI uses Qt6 through `github.com/mappu/miqt/qt6` from MIQT
v0.15.0. Use a Go toolchain compatible with the `go 1.23` requirement in
[`go.mod`](go.mod), enable CGO, and build with both production tags:

```bash
CGO_ENABLED=1 go build -tags "miqt serial" ./cmd/ddgo
```

The Ubuntu CI build installs these development prerequisites:

```bash
sudo apt-get install build-essential pkg-config qt6-base-dev qt6-base-dev-tools
```

Notes:

- The serial transport uses `go.bug.st/serial`.
- The UI is written directly against `github.com/mappu/miqt/qt6`.
- The no-tag build prints an error telling you to rebuild with tags.
- macOS and Windows setup, packaging, and Qt runtime details are maintained in
  the platform-specific [CI/CD configuration](.github/workflows/cd.yml), not
  duplicated here.

## Development status

Current roadmap:

- Preserve the behavior of each currently supported macro explicitly identified by the corrected command list. M103-M105 are intentionally undefined. M110 is unsupported legacy functionality and does not need to be completed or preserved as a working feature.
- Add configurable machine profile support.
- Add persistence/settings as needed.
- Replace the 3D toolpath placeholder and continue improving document UI affordances.
- Add guided package-workflow behavior only when its machine-safety semantics are defined.
