#!/usr/bin/env bash
set -euxo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
source "$script_dir/macos-macho-deps.sh"

if [[ $# -lt 3 ]]; then
  echo "usage: $0 <arm64|amd64> <input-binary> <output-app> [version]" >&2
  exit 1
fi

arch="$1"
bin_path="$2"
app_dir="$3"
version="${4:-0.0.0}"

case "$arch" in
  arm64) macho_arch=arm64 ;;
  amd64) macho_arch=x86_64 ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

if [[ ! -f "$bin_path" ]]; then
  echo "input binary not found: $bin_path" >&2
  exit 1
fi

qt_prefix="${QT_ROOT:?QT_ROOT must name the pinned Qt distribution}"
qt_prefix="$(cd "$qt_prefix" && pwd -P)"
case "$qt_prefix" in
  /opt/homebrew/*|/usr/local/*)
    echo "QT_ROOT must not resolve to Homebrew: $qt_prefix" >&2
    exit 1
    ;;
esac
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-15.0}"
export PATH="$qt_prefix/bin:$PATH"
export PKG_CONFIG_PATH="${QT_PKG_CONFIG_DIR:?QT_PKG_CONFIG_DIR must describe QT_ROOT}"
export PKG_CONFIG_LIBDIR="$PKG_CONFIG_PATH"
qt_version="${QT_VERSION:?QT_VERSION must identify the pinned Qt distribution}"
qt_distribution="${QT_DISTRIBUTION:?QT_DISTRIBUTION must identify the pinned Qt distribution}"
aqtinstall_version="${AQTINSTALL_VERSION:?AQTINSTALL_VERSION must identify the installer}"

test -x "$qt_prefix/bin/qmake"
test -x "$qt_prefix/bin/macdeployqt"
if [[ "$("$qt_prefix/bin/qmake" -query QT_VERSION)" != "$qt_version" ]]; then
  echo "QT_ROOT version does not match QT_VERSION: $qt_prefix" >&2
  exit 1
fi

if [[ "$version" == "master" ]]; then
  bundle_version="0.0.0"
else
  bundle_version="${version#v}"
  bundle_version="${bundle_version%%[-+]*}"
fi
if [[ ! "$bundle_version" =~ ^[0-9]+([.][0-9]+){0,2}$ ]]; then
  bundle_version="0.0.0"
fi
short_version="$bundle_version"

rm -rf "$app_dir"
mkdir -p "$app_dir/Contents/MacOS" "$app_dir/Contents/Resources"

cat > "$app_dir/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>ddgo</string>
<key>CFBundleIdentifier</key><string>com.ianbruene.ddgo</string>
<key>CFBundleName</key><string>DDGo</string>
<key>CFBundleDisplayName</key><string>DDGo</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>${short_version}</string>
<key>CFBundleVersion</key><string>${bundle_version}</string>
<key>LSMinimumSystemVersion</key><string>${MACOSX_DEPLOYMENT_TARGET}</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST

ditto "$bin_path" "$app_dir/Contents/MacOS/ddgo"
chmod +x "$app_dir/Contents/MacOS/ddgo"

cat > "$app_dir/Contents/Resources/ddgo-qt-build-metadata.txt" <<EOF
distribution=$qt_distribution
aqtinstall_version=$aqtinstall_version
qt_version=$qt_version
EOF

test -f "$app_dir/Contents/Info.plist"
test -x "$app_dir/Contents/MacOS/ddgo"
/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$app_dir/Contents/Info.plist"
file "$app_dir/Contents/MacOS/ddgo"
lipo -archs "$app_dir/Contents/MacOS/ddgo"
otool -L "$app_dir/Contents/MacOS/ddgo"

echo "qt_prefix=$qt_prefix"
echo "qt_version=$qt_version"
echo "qt_distribution=$qt_distribution"
echo "MACOSX_DEPLOYMENT_TARGET=$MACOSX_DEPLOYMENT_TARGET"
echo "PATH=$PATH"
echo "PKG_CONFIG_PATH=$PKG_CONFIG_PATH"
test -x "$qt_prefix/bin/macdeployqt"
"$qt_prefix/bin/macdeployqt" -version || true
otool -L "$app_dir/Contents/MacOS/ddgo"

"$qt_prefix/bin/macdeployqt" "$app_dir" -verbose=2

find "$app_dir/Contents" -maxdepth 5 -print | sort
find "$app_dir/Contents" -type f -name "*.dylib" -print | sort
find "$app_dir/Contents" -type f -name "libqcocoa.dylib" -print
test -f "$app_dir/Contents/PlugIns/platforms/libqcocoa.dylib"

# macdeployqt deploys the frameworks but does not consistently provide a
# run-path for MIQT's @rpath framework references in this official-Qt layout.
# Inspect first, repair the executable, then touch a plugin only if it still
# has an unresolved required @rpath dependency in the final bundle.
app_executable="$app_dir/Contents/MacOS/ddgo"
echo "runtime paths before correction for $app_executable ($macho_arch):"
macos_list_rpaths "$app_executable" "$macho_arch"
macos_add_rpath_if_missing "$app_executable" '@executable_path/../Frameworks' "$macho_arch"

plugin_has_unresolved_rpath() {
  local plugin="$1"
  local load_command
  local dependency
  while IFS='|' read -r load_command dependency; do
    [[ "$dependency" == @rpath/* ]] || continue
    macos_is_weak_load_command "$load_command" && continue
    if ! macos_resolve_dependency "$app_dir" "$app_executable" "$plugin" "$dependency" "$macho_arch" >/dev/null; then
      return 0
    fi
  done < <(macos_list_load_commands "$plugin" "$macho_arch")
  return 1
}

for plugin_dir in "$app_dir/Contents/PlugIns" "$app_dir/Contents/Plugins"; do
  [[ -d "$plugin_dir" ]] || continue
  while IFS= read -r -d '' plugin; do
    file "$plugin" | grep -q 'Mach-O' || continue
    if plugin_has_unresolved_rpath "$plugin"; then
      echo "runtime paths before correction for plugin $plugin ($macho_arch):"
      macos_list_rpaths "$plugin" "$macho_arch"
      macos_add_rpath_if_missing "$plugin" '@loader_path/../../Frameworks' "$macho_arch"
    fi
  done < <(find "$plugin_dir" -type f -print0)
done

echo "verifying packaged runtime dependencies before signing ($macho_arch)"
macos_verify_app_dependencies "$app_dir" "$app_executable" "$macho_arch"

codesign --force --deep --sign - "$app_dir"
codesign --verify --deep --strict --verbose=2 "$app_dir"

echo "created app bundle at $app_dir"
