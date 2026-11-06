#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <qt-download-root> <expected-qt-version> <required-architecture>" >&2
  exit 1
fi

qt_download_root="$1"
expected_version="$2"
required_arch="$3"

if [[ "$(uname -s)" != "Darwin" ]]; then
  fail "macOS Qt installation validation requires Darwin"
fi
if ! qt_download_root="$(cd "$qt_download_root" && pwd -P)"; then
  fail "Qt download root does not exist: $qt_download_root"
fi

qmake_candidates=()
while IFS= read -r candidate; do
  qmake_candidates+=("$candidate")
done < <(find "$qt_download_root/$expected_version" -path '*/bin/qmake' -type f -print | sort)
if [[ ${#qmake_candidates[@]} -ne 1 ]]; then
  echo "expected exactly one qmake below $qt_download_root/$expected_version, found ${#qmake_candidates[@]}" >&2
  printf '%s\n' "${qmake_candidates[@]:-}" >&2
  exit 1
fi

qmake="${qmake_candidates[0]}"
if [[ ! -x "$qmake" ]]; then
  fail "qmake is not executable: $qmake"
fi
qt_root="$(cd "$(dirname "$(dirname "$qmake")")" && pwd -P)"
case "$qt_root" in
  /opt/homebrew/*|/usr/local/*)
    fail "pinned Qt unexpectedly resolved to Homebrew: $qt_root"
    ;;
esac

qmake_query() {
  local key="$1"
  local value
  if ! value="$("$qmake" -query "$key")" || [[ -z "$value" ]]; then
    fail "qmake query failed or returned an empty value for $key: $qmake"
  fi
  printf '%s\n' "$value"
}

qt_version="$(qmake_query QT_VERSION)"
[[ "$qt_version" == "$expected_version" ]] ||
  fail "Qt version is $qt_version, expected $expected_version ($qmake)"

qt_prefix="$(qmake_query QT_INSTALL_PREFIX)"
[[ "$qt_prefix" == "$qt_root" ]] ||
  fail "cached Qt prefix is not relocatable to this runner: qmake reports $qt_prefix, actual root is $qt_root"

qt_lib_dir="$(qmake_query QT_INSTALL_LIBS)"
qt_include_dir="$(qmake_query QT_INSTALL_HEADERS)"
qt_archdata_dir="$(qmake_query QT_INSTALL_ARCHDATA)"
for required_dir in "$qt_lib_dir" "$qt_include_dir" "$qt_archdata_dir/mkspecs"; do
  [[ -d "$required_dir" ]] || fail "missing Qt directory reported by qmake: $required_dir"
  case "$required_dir" in
    "$qt_root"/*) ;;
    *) fail "qmake directory is outside the restored Qt root: $required_dir (root $qt_root)" ;;
  esac
done

macdeployqt="$qt_root/bin/macdeployqt"
[[ -x "$macdeployqt" ]] || fail "macdeployqt is not executable: $macdeployqt"

for framework in QtCore QtGui QtWidgets; do
  framework_dir="$qt_lib_dir/$framework.framework"
  framework_headers="$framework_dir/Headers"
  framework_binary="$framework_dir/$framework"
  [[ -d "$framework_headers" ]] || fail "missing framework headers: $framework_headers"
  [[ -e "$framework_binary" ]] || fail "missing or dangling framework binary: $framework_binary"
  framework_archs="$(lipo -archs "$framework_binary")"
  case " $framework_archs " in
    *" $required_arch "*) ;;
    *) fail "$framework_binary does not contain $required_arch: $framework_archs" ;;
  esac
  while IFS= read -r -d '' link; do
    [[ -e "$link" ]] || fail "dangling framework symlink: $link"
  done < <(find "$framework_dir" -type l -print0)
done

# If official pkg-config metadata is present, it must not carry stale Homebrew
# or other external installation paths. configure-macos-qt.sh performs the
# final module-level validation and generates a fresh local overlay.
official_pc_dir="$qt_lib_dir/pkgconfig"
if [[ -d "$official_pc_dir" ]] && grep -R -E -q '/opt/homebrew|/usr/local' "$official_pc_dir"; then
  fail "official Qt pkg-config metadata references Homebrew: $official_pc_dir"
fi
if [[ -d "$official_pc_dir" ]]; then
  for module in Qt6Core Qt6Gui Qt6Widgets; do
    pc_file="$official_pc_dir/$module.pc"
    [[ -f "$pc_file" ]] || continue
    if ! module_prefix="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$official_pc_dir" pkg-config --variable=prefix "$module")"; then
      fail "cannot read official Qt pkg-config prefix for $module: $pc_file"
    fi
    [[ "$module_prefix" == "$qt_root" ]] ||
      fail "official Qt pkg-config prefix for $module is not the restored root: $module_prefix (root $qt_root)"
  done
fi

# Qt's CMake package records are normally relocation-safe through paths
# relative to their own config files. Require the component records used by
# DDGo to be present and reject an explicitly Homebrew-patched installation.
qt_cmake_dir="$qt_lib_dir/cmake"
for module in Qt6Core Qt6Gui Qt6Widgets; do
  cmake_config="$qt_cmake_dir/$module/${module}Config.cmake"
  [[ -f "$cmake_config" ]] || fail "missing Qt CMake package metadata: $cmake_config"
  if grep -R -E -q '/opt/homebrew|/usr/local' "$qt_cmake_dir/$module"; then
    fail "Qt CMake package metadata references Homebrew: $qt_cmake_dir/$module"
  fi
done

printf 'QT_ROOT=%s\n' "$qt_root"
