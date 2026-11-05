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

# Exercise the shared load-command and rpath parsers without requiring macOS
# tools. Real bundle verification still uses the runner's otool output.
otool() {
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
macos_is_weak_load_command LC_LOAD_WEAK_DYLIB || fail "weak load command was not recognized"
if macos_is_weak_load_command LC_LOAD_DYLIB; then
  fail "required load command was treated as weak"
fi
unset -f otool

tmpdir="$(mktemp -d)"
cleanup() {
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

cache_line="$(grep 'key: ddgo-macos-build-v2-' "$repo_root/.github/workflows/cd.yml")"
for excluded in "**/*.go" "macos-macho-deps.sh" "test-macos-toolchain.sh"; do
  [[ "$cache_line" != *"$excluded"* ]] || fail "packaging-only or source input remains in macOS build-cache key: $excluded"
done
for required in "go.mod" "go.sum" "configure-macos-qt.sh" 'steps.setup-go.outputs.go-version' 'env.QT_VERSION' 'env.QT_DISTRIBUTION' 'env.AQTINSTALL_VERSION' 'matrix.macos_min' 'sdk-' 'clang-'; do
  [[ "$cache_line" == *"$required"* ]] || fail "macOS build-cache key is missing $required"
done

echo "macOS toolchain static regression checks passed"
