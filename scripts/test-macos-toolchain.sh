#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
source "$repo_root/scripts/macos-macho-deps.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

expect_resolves() {
  local app="$1"
  local executable="$2"
  local loader="$3"
  local dependency="$4"
  macos_resolve_dependency "$app" "$executable" "$loader" "$dependency" >/dev/null ||
    fail "expected dependency to resolve: $dependency"
}

validate_qt_environment() {
  local environment_file="$1"
  local expected_pc_dir="$2"
  local expected_source="$3"
  local expected_version="$4"
  local line
  local actual_pc_dir=""
  local actual_source=""
  local actual_version=""
  local actual_pc_dir_canonical=""
  local expected_pc_dir_canonical=""
  local pc_dir_count=0
  local source_count=0
  local version_count=0

  while IFS= read -r line || [[ -n "$line" ]]; do
    case "$line" in
      QT_PKG_CONFIG_DIR=*)
        pc_dir_count=$((pc_dir_count + 1))
        actual_pc_dir="${line#QT_PKG_CONFIG_DIR=}"
        ;;
      QT_PKG_CONFIG_SOURCE=*)
        source_count=$((source_count + 1))
        actual_source="${line#QT_PKG_CONFIG_SOURCE=}"
        ;;
      QT_VERSION=*)
        version_count=$((version_count + 1))
        actual_version="${line#QT_VERSION=}"
        ;;
      *)
        echo "expected only machine-readable Qt environment keys; unexpected stdout: $line" >&2
        cat "$environment_file" >&2
        fail "Qt configuration stdout contract changed"
        ;;
    esac
  done < "$environment_file"

  if [[ $pc_dir_count -ne 1 || $source_count -ne 1 || $version_count -ne 1 ]]; then
    echo "expected one QT_PKG_CONFIG_DIR, QT_PKG_CONFIG_SOURCE, and QT_VERSION entry" >&2
    cat "$environment_file" >&2
    fail "Qt configuration stdout contract changed"
  fi
  if [[ ! -d "$actual_pc_dir" ]]; then
    echo "Qt configuration reported a nonexistent QT_PKG_CONFIG_DIR: $actual_pc_dir" >&2
    cat "$environment_file" >&2
    fail "Qt configuration stdout contract changed"
  fi
  expected_pc_dir_canonical="$(cd "$expected_pc_dir" && pwd -P)"
  actual_pc_dir_canonical="$(cd "$actual_pc_dir" && pwd -P)"
  if [[ "$actual_pc_dir_canonical" != "$expected_pc_dir_canonical" || "$actual_source" != "$expected_source" || "$actual_version" != "$expected_version" ]]; then
    echo "expected QT_PKG_CONFIG_DIR=$expected_pc_dir_canonical" >&2
    echo "actual QT_PKG_CONFIG_DIR=$actual_pc_dir (canonical: $actual_pc_dir_canonical)" >&2
    echo "expected QT_PKG_CONFIG_SOURCE=$expected_source; actual=$actual_source" >&2
    echo "expected QT_VERSION=$expected_version; actual=$actual_version" >&2
    echo "complete Qt configuration stdout:" >&2
    cat "$environment_file" >&2
    fail "Qt configuration stdout contract changed"
  fi
}

# Exercise the shared load-command and rpath parsers without requiring macOS
# tools. Real bundle verification still uses the runner's otool output.
otool() {
  if [[ "$1" == "-arch" ]]; then
    [[ "$2" == "arm64" || "$2" == "x86_64" ]] || exit 2
    shift 2
  fi
  cat <<'EOF'
Load command 1
          cmd LC_LOAD_DYLIB
      cmdsize 56
         name @rpath/QtCore.framework/QtCore (offset 24)
Load command 2
          cmd LC_LOAD_WEAK_DYLIB
      cmdsize 56
         name @rpath/Optional.framework/Optional (offset 24)
Load command 3
          cmd LC_REEXPORT_DYLIB
      cmdsize 56
         name @loader_path/libShared.dylib (offset 24)
Load command 4
          cmd LC_RPATH
      cmdsize 48
         path @executable_path/../Frameworks (offset 12)
EOF
}

load_commands="$(macos_list_load_commands ignored)"
[[ "$load_commands" == *'LC_LOAD_DYLIB|@rpath/QtCore.framework/QtCore'* ]] || fail "LC_LOAD_DYLIB was not parsed"
[[ "$load_commands" == *'LC_LOAD_WEAK_DYLIB|@rpath/Optional.framework/Optional'* ]] || fail "LC_LOAD_WEAK_DYLIB was not parsed"
[[ "$load_commands" == *'LC_REEXPORT_DYLIB|@loader_path/libShared.dylib'* ]] || fail "LC_REEXPORT_DYLIB was not parsed"
[[ "$(macos_list_rpaths ignored)" == '@executable_path/../Frameworks' ]] || fail "LC_RPATH was not parsed"
[[ "$(macos_list_rpaths ignored arm64)" == '@executable_path/../Frameworks' ]] || fail "architecture-selected LC_RPATH was not parsed"
macos_is_weak_load_command LC_LOAD_WEAK_DYLIB || fail "weak load command was not recognized"
if macos_is_weak_load_command LC_LOAD_DYLIB; then
  fail "required load command was treated as weak"
fi
unset -f otool

tmpdir="$(mktemp -d)"
tmpdir="$(cd "$tmpdir" && pwd -P)"
cleanup() {
  rm -f "${tmpdir_link:-}"
  rm -rf "$tmpdir"
}
trap cleanup EXIT

app="$tmpdir/DDGo.app"
exe="$app/Contents/MacOS/ddgo"
plugin="$app/Contents/PlugIns/platforms/libqcocoa.dylib"
framework="$app/Contents/Frameworks/QtCore.framework"

mkdir -p "$(dirname "$exe")" "$(dirname "$plugin")" "$framework/Versions/A"
: > "$exe"
: > "$plugin"
: > "$framework/Versions/A/QtCore"
ln -s A "$framework/Versions/Current"
ln -s Versions/Current/QtCore "$framework/QtCore"

# The plugin has no local rpath; its framework dependency is found through the
# main executable's run-path entry, as in a normal packaged Qt application.
# Replace the otool-backed enumerator with deterministic representative paths.
macos_list_rpaths() {
  if [[ "$1" == "$exe" ]]; then
    printf '%s\n' '@executable_path/../Frameworks'
  fi
}

expect_resolves "$app" "$exe" "$plugin" '@loader_path/../../Frameworks/QtCore.framework/QtCore'
expect_resolves "$app" "$exe" "$plugin" '@executable_path/../Frameworks/QtCore.framework/QtCore'
expect_resolves "$app" "$exe" "$plugin" '@rpath/QtCore.framework/QtCore'
expect_resolves "$app" "$exe" "$plugin" '@rpath/QtCore.framework/Versions/A/QtCore'
macos_has_rpath "$exe" '@executable_path/../Frameworks' arm64 || fail "executable runtime path was not recognized"
if macos_has_rpath "$plugin" '@loader_path/../../Frameworks' arm64; then
  fail "plugin unexpectedly had a loader-relative runtime path"
fi

install_name_tool() {
  fail "duplicate runtime-path repair invoked install_name_tool"
}
macos_add_rpath_if_missing "$exe" '@executable_path/../Frameworks' arm64
install_name_tool() {
  installed_rpath_args="$*"
}
macos_add_rpath_if_missing "$plugin" '@loader_path/../../Frameworks' arm64
[[ "$installed_rpath_args" == "-add_rpath @loader_path/../../Frameworks $plugin" ]] ||
  fail "missing plugin runtime path was not added with the expected arguments"
unset -f install_name_tool

outside="$tmpdir/outside.dylib"
: > "$outside"
if macos_resolve_dependency "$app" "$exe" "$plugin" '@loader_path/../../../../outside.dylib' >/dev/null; then
  fail "@loader_path dependency escaping the bundle resolved"
fi
if macos_resolve_dependency "$app" "$exe" "$plugin" '@executable_path/../../../outside.dylib' >/dev/null; then
  fail "@executable_path dependency escaping the bundle resolved"
fi

mkdir -p "$app/Contents/Resources/Missing.framework"
: > "$app/Contents/Resources/Missing.framework/Missing"
for slice in arm64 x86_64; do
  if macos_resolve_dependency "$app" "$exe" "$plugin" '@rpath/Missing.framework/Missing' >/dev/null; then
    fail "similarly named library outside the declared rpath resolved for $slice slice"
  fi
done
ln -s Versions/Current/Missing "$framework/Dangling"
if macos_resolve_dependency "$app" "$exe" "$plugin" '@loader_path/../../Frameworks/QtCore.framework/Dangling' >/dev/null; then
  fail "dangling framework symlink resolved"
fi
rm "$framework/Dangling"

macos_is_system_dependency '/usr/lib/libSystem.B.dylib' || fail "system library was rejected"
macos_is_system_dependency '/System/Library/Frameworks/AppKit.framework/AppKit' || fail "system framework was rejected"
if macos_is_system_dependency '/usr/lib/../../tmp/escape.dylib'; then
  fail "system-library traversal path was accepted"
fi
macos_is_homebrew_dependency '/opt/homebrew/opt/qt/lib/QtCore.framework/QtCore' || fail "Homebrew arm64 path was accepted"
macos_is_homebrew_dependency '/usr/local/opt/qt/lib/QtCore.framework/QtCore' || fail "Homebrew Intel path was accepted"
if macos_resolve_dependency "$app" "$exe" "$plugin" '/opt/homebrew/opt/qt/lib/QtCore.framework/QtCore' >/dev/null; then
  fail "external Homebrew dependency resolved as bundled"
fi
macos_has_arch 'arm64 x86_64' arm64 || fail "arm64 was not detected"
macos_has_arch 'arm64 x86_64' x86_64 || fail "x86_64 was not detected"
if macos_has_arch 'arm64 x86_64h' x86_64; then
  fail "x86_64h was accepted as x86_64"
fi

# The packaging preflight keeps unresolved required dependencies fatal and
# unresolved weak dependencies as warnings. Use the real fixture filesystem
# with lightweight load-command and Mach-O probes.
file() {
  printf '%s\n' 'Mach-O 64-bit dynamically linked shared library'
}
macos_list_rpaths() {
  return 0
}
macos_resolve_dependency() {
  return 1
}
macos_list_load_commands() {
  if [[ "$1" == "$exe" ]]; then
    printf '%s\n' 'LC_LOAD_DYLIB|@rpath/Missing.framework/Missing'
  fi
}
if macos_verify_app_dependencies "$app" "$exe" arm64 > "$tmpdir/preflight-required.out" 2> "$tmpdir/preflight-required.err"; then
  fail "runtime preflight accepted an unresolved required dependency"
fi
grep -Fq 'unresolved required dependency' "$tmpdir/preflight-required.err" ||
  fail "runtime preflight did not report the unresolved required dependency"
macos_list_load_commands() {
  if [[ "$1" == "$exe" ]]; then
    printf '%s\n' 'LC_LOAD_WEAK_DYLIB|@rpath/Optional.framework/Optional'
  fi
}
if ! macos_verify_app_dependencies "$app" "$exe" arm64 > "$tmpdir/preflight-weak.out" 2> "$tmpdir/preflight-weak.err"; then
  fail "runtime preflight rejected an unresolved weak dependency"
fi
grep -Fq 'unresolved weak dependency' "$tmpdir/preflight-weak.err" ||
  fail "runtime preflight did not warn about the unresolved weak dependency"
ln -s Versions/Current/Missing "$framework/Dangling"
if macos_verify_app_dependencies "$app" "$exe" arm64 > "$tmpdir/preflight-symlink.out" 2> "$tmpdir/preflight-symlink.err"; then
  fail "runtime preflight accepted a dangling framework symlink"
fi
grep -Fq 'dangling bundled symlink' "$tmpdir/preflight-symlink.err" ||
  fail "runtime preflight did not report the dangling framework symlink"
rm "$framework/Dangling"
unset -f file

cache_line="$(grep 'key: ddgo-macos-build-v2-' "$repo_root/.github/workflows/cd.yml")"
for excluded in "**/*.go" "macos-macho-deps.sh" "test-macos-toolchain.sh"; do
  [[ "$cache_line" != *"$excluded"* ]] || fail "packaging-only or source input remains in macOS build-cache key: $excluded"
done
for required in "go.mod" "go.sum" "configure-macos-qt.sh" 'steps.setup-go.outputs.go-version' 'env.QT_VERSION' 'env.QT_DISTRIBUTION' 'env.AQTINSTALL_VERSION' 'matrix.macos_min' 'sdk-' 'clang-'; do
  [[ "$cache_line" == *"$required"* ]] || fail "macOS build-cache key is missing $required"
done

# Run the actual Qt configuration helper against a lightweight fake qmake
# installation. This validates framework naming and generated metadata without
# claiming to validate real macOS linking.
command -v pkg-config >/dev/null || fail "pkg-config is required for Qt configuration regression coverage"
qt_fixture="$tmpdir/qt"
for framework_name in QtCore QtGui QtWidgets; do
  framework_dir="$qt_fixture/lib/$framework_name.framework"
  mkdir -p "$framework_dir/Versions/A/Headers"
  : > "$framework_dir/Versions/A/$framework_name"
  ln -s Versions/A/Headers "$framework_dir/Headers"
  ln -s Versions/A/$framework_name "$framework_dir/$framework_name"
done
mkdir -p "$qt_fixture/bin" "$qt_fixture/include" "$qt_fixture/archdata/mkspecs" "$tmpdir/fake-bin"
cat > "$qt_fixture/bin/qmake" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

qt_root="$(cd "$(dirname "$0")/.." && pwd -P)"
[[ "$1" == "-query" ]] || exit 2
case "$2" in
  QT_VERSION) printf '%s\n' '6.12.0' ;;
  QT_INSTALL_LIBS) printf '%s\n' "$qt_root/lib" ;;
  QT_INSTALL_HEADERS) printf '%s\n' "$qt_root/include" ;;
  QT_INSTALL_ARCHDATA) printf '%s\n' "$qt_root/archdata" ;;
  *) exit 3 ;;
esac
EOF
cat > "$qt_fixture/bin/macdeployqt" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat > "$tmpdir/fake-bin/uname" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == "-s" ]]; then
  printf '%s\n' Darwin
  exit 0
fi
exec /usr/bin/uname "$@"
EOF
chmod +x "$qt_fixture/bin/qmake" "$qt_fixture/bin/macdeployqt" "$tmpdir/fake-bin/uname"

qt_environment="$tmpdir/qt-environment"
qt_diagnostics="$tmpdir/qt-diagnostics"
generated_pc_dir="$tmpdir/generated-pc"
if ! PATH="$tmpdir/fake-bin:$PATH" bash "$repo_root/scripts/configure-macos-qt.sh" "$qt_fixture" 6.12.0 "$generated_pc_dir" > "$qt_environment" 2> "$qt_diagnostics"; then
  cat "$qt_diagnostics" >&2
  fail "Qt configuration helper rejected valid Qt framework fixture"
fi
validate_qt_environment "$qt_environment" "$generated_pc_dir" generated 6.12.0
for framework_name in QtCore QtGui QtWidgets; do
  module="Qt6${framework_name#Qt}"
  pc_file="$generated_pc_dir/$module.pc"
  grep -Fq "$framework_name.framework/Headers" "$pc_file" || fail "$module metadata used the wrong framework headers"
  grep -Fq 'Cflags: -I${libdir}/' "$pc_file" || fail "$module metadata did not put framework headers first"
  grep -Fq -- '-F${libdir}' "$pc_file" || fail "$module metadata omitted the framework search path"
  grep -Fq -- "-framework $framework_name" "$pc_file" || fail "$module metadata used the wrong framework linker flag"
done

# The helper accepts a valid fixture through a symlink, but returns a canonical
# metadata path. This covers macOS /var-to-/private/var path normalization.
tmpdir_link="${tmpdir}-link"
ln -s "$tmpdir" "$tmpdir_link"
symlinked_environment="$tmpdir/symlinked-qt-environment"
symlinked_pc_dir="$tmpdir_link/symlinked-generated-pc"
if ! PATH="$tmpdir/fake-bin:$PATH" bash "$repo_root/scripts/configure-macos-qt.sh" "$tmpdir_link/qt" 6.12.0 "$symlinked_pc_dir" > "$symlinked_environment" 2> "$tmpdir/symlinked-qt-diagnostics"; then
  cat "$tmpdir/symlinked-qt-diagnostics" >&2
  fail "Qt configuration helper rejected a symlinked valid Qt fixture"
fi
validate_qt_environment "$symlinked_environment" "$tmpdir/symlinked-generated-pc" generated 6.12.0

# Official metadata is preserved in a local overlay. The fixture deliberately
# models the module-local include order that fails MIQT's <QtCore/...> headers.
official_pc_dir="$qt_fixture/lib/pkgconfig"
mkdir -p "$official_pc_dir"
cat > "$official_pc_dir/Qt6Platform.pc" <<EOF
prefix=$qt_fixture
libdir=\${prefix}/lib
includedir=\${prefix}/include

Name: Qt6 Platform
Description: fixture platform metadata
Version: 6.12.0
Cflags: -I\${includedir}
EOF
for framework_name in QtCore QtGui QtWidgets; do
  module="Qt6${framework_name#Qt}"
  case "$framework_name" in
    QtCore) requires=Qt6Platform ;;
    QtGui) requires=Qt6Core ;;
    QtWidgets) requires="Qt6Core Qt6Gui" ;;
  esac
  cat > "$official_pc_dir/$module.pc" <<EOF
prefix=$qt_fixture
libdir=\${prefix}/lib
includedir=\${prefix}/include

Name: $module
Description: fixture official metadata
Version: 6.12.0
Libs: -F\${libdir} -framework $framework_name -lpreserved${framework_name#Qt}
Cflags: -I\${includedir}/$framework_name -I\${libdir}/$framework_name.framework -DOFFICIAL_${framework_name#Qt}
Requires: $requires
EOF
done

official_overlay_environment="$tmpdir/official-overlay-environment"
official_overlay_pc_dir="$tmpdir/official-overlay-pc"
if ! PATH="$tmpdir/fake-bin:$PATH" bash "$repo_root/scripts/configure-macos-qt.sh" "$qt_fixture" 6.12.0 "$official_overlay_pc_dir" > "$official_overlay_environment" 2> "$tmpdir/official-overlay-diagnostics"; then
  cat "$tmpdir/official-overlay-diagnostics" >&2
  fail "Qt configuration helper rejected valid official metadata fixture"
fi
validate_qt_environment "$official_overlay_environment" "$official_overlay_pc_dir" official-overlay 6.12.0
core_cflags="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$official_overlay_pc_dir" pkg-config --cflags Qt6Core)"
case "$core_cflags" in
  *"-I$qt_fixture/lib/QtCore.framework/Headers"*"-I$qt_fixture/include/QtCore"*) ;;
  *) fail "official overlay did not prepend QtCore framework headers and search path: $core_cflags" ;;
esac
[[ "$core_cflags" == *"-F$qt_fixture/lib"* ]] ||
  fail "official overlay omitted QtCore framework search path: $core_cflags"
[[ "$core_cflags" == *'-DOFFICIAL_Core'* ]] ||
  fail "official Qt compiler flags were not preserved"
widget_cflags="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$official_overlay_pc_dir" pkg-config --cflags Qt6Widgets)"
for framework_name in QtCore QtGui QtWidgets; do
  [[ "$widget_cflags" == *"$qt_fixture/lib/$framework_name.framework/Headers"* ]] ||
    fail "Qt6Widgets flags omitted $framework_name framework headers"
done
[[ "$widget_cflags" != *"/opt/homebrew/"* && "$widget_cflags" != *"/usr/local/"* ]] ||
  fail "official overlay introduced Homebrew Qt include paths"
official_widget_libs="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$official_overlay_pc_dir" pkg-config --libs Qt6Widgets)"
[[ "$official_widget_libs" == *"-lpreservedCore"* ]] || fail "official Qt linker flags were not preserved"

if PATH="$tmpdir/fake-bin:$PATH" bash "$repo_root/scripts/configure-macos-qt.sh" "$tmpdir/missing-qt" 6.12.0 "$tmpdir/missing-pc" > "$tmpdir/missing-environment" 2> "$tmpdir/missing-diagnostics"; then
  fail "Qt configuration accepted a nonexistent Qt root"
fi
grep -Fq 'Qt root does not exist or is not a directory' "$tmpdir/missing-diagnostics" ||
  fail "nonexistent Qt root diagnostic was not actionable"

# A Core.framework lookalike must not satisfy Qt6Core's QtCore.framework
# requirement. The helper's stderr remains visible even when stdout is used as
# a machine-readable environment file.
mv "$qt_fixture/lib/QtCore.framework" "$qt_fixture/lib/QtCore.framework.missing"
mkdir -p "$qt_fixture/lib/Core.framework/Headers"
: > "$qt_fixture/lib/Core.framework/Core"
if PATH="$tmpdir/fake-bin:$PATH" bash "$repo_root/scripts/configure-macos-qt.sh" "$qt_fixture" 6.12.0 "$tmpdir/invalid-pc" > "$tmpdir/invalid-environment" 2> "$tmpdir/invalid-diagnostics"; then
  fail "Qt configuration accepted Core.framework as Qt6Core"
fi
grep -Fq 'missing required framework headers for Qt6Core (QtCore)' "$tmpdir/invalid-diagnostics" ||
  fail "missing framework diagnostic was not actionable"

echo "macOS toolchain static regression checks passed"
