#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <qt-root> <expected-qt-version> <pkg-config-output-dir>" >&2
  exit 1
fi

qt_root="$1"
expected_version="$2"
pkgconfig_dir="$3"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS Qt configuration requires Darwin" >&2
  exit 1
fi

qt_root="$(cd "$qt_root" && pwd -P)"
case "$qt_root" in
  /opt/homebrew/*|/usr/local/*)
    echo "QT_ROOT must be the pinned Qt distribution, not Homebrew: $qt_root" >&2
    exit 1
    ;;
esac

qmake="$qt_root/bin/qmake"
macdeployqt="$qt_root/bin/macdeployqt"
test -x "$qmake"
test -x "$macdeployqt"

qt_version="$($qmake -query QT_VERSION)"
if [[ "$qt_version" != "$expected_version" ]]; then
  echo "Qt version is $qt_version, expected $expected_version" >&2
  exit 1
fi

qt_lib_dir="$($qmake -query QT_INSTALL_LIBS)"
qt_include_dir="$($qmake -query QT_INSTALL_HEADERS)"
qt_archdata_dir="$($qmake -query QT_INSTALL_ARCHDATA)"
test -d "$qt_lib_dir"
test -d "$qt_include_dir"
test -d "$qt_archdata_dir/mkspecs"

required_pkg_modules=(Qt6Platform Qt6Core Qt6Gui Qt6Widgets)
required_framework_modules=(Qt6Core Qt6Gui Qt6Widgets)
for module in "${required_framework_modules[@]}"; do
  framework="${module#Qt6}"
  test -d "$qt_lib_dir/$framework.framework/Headers"
  test -e "$qt_lib_dir/$framework.framework/$framework"
done

# Qt's official macOS packages do not guarantee pkg-config metadata. Prefer
# suitable metadata when present; otherwise create only the Qt base-module
# records MIQT v0.15.0 needs. The records derive every path and version from
# qmake, so they cannot point at a separately installed Homebrew Qt.
official_pc_dir="$qt_lib_dir/pkgconfig"
use_official=1
for module in "${required_pkg_modules[@]}"; do
  if [[ ! -f "$official_pc_dir/$module.pc" ]]; then
    use_official=0
    break
  fi
done

if [[ $use_official -eq 1 ]]; then
  for module in "${required_pkg_modules[@]}"; do
    if ! module_version="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$official_pc_dir" pkg-config --modversion "$module")" ||
      ! module_prefix="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$official_pc_dir" pkg-config --variable=prefix "$module")"; then
      use_official=0
      break
    fi
    if [[ "$module_version" != "$expected_version" || "$module_prefix" != "$qt_root" ]]; then
      use_official=0
      break
    fi
  done
fi

if [[ $use_official -eq 1 ]]; then
  selected_pc_dir="$official_pc_dir"
  pc_source="official"
else
  rm -rf "$pkgconfig_dir"
  mkdir -p "$pkgconfig_dir"
  selected_pc_dir="$(cd "$pkgconfig_dir" && pwd -P)"
  pc_source="generated"

  cat > "$selected_pc_dir/Qt6Platform.pc" <<EOF
prefix=$qt_root
libdir=$qt_lib_dir
includedir=$qt_include_dir
mkspecsdir=$qt_archdata_dir/mkspecs

Name: Qt6 Platform
Description: Qt 6 macOS platform settings derived from qmake
Version: $qt_version
Cflags: -I\${mkspecsdir}/macx-clang -I\${includedir}
EOF

  write_framework_pc() {
    local module="$1"
    local requires="$2"
    local define="$3"
    local framework="${module#Qt6}"
    cat > "$selected_pc_dir/$module.pc" <<EOF
prefix=$qt_root
libdir=$qt_lib_dir
includedir=$qt_include_dir

Name: $module
Description: Qt 6 $framework framework derived from qmake
Version: $qt_version
Libs: -F\${libdir} -framework $framework
Cflags: -I\${libdir}/$framework.framework/Headers -I\${libdir}/$framework.framework -D$define
Requires: $requires
EOF
  }

  write_framework_pc Qt6Core Qt6Platform QT_CORE_LIB
  write_framework_pc Qt6Gui Qt6Core QT_GUI_LIB
  write_framework_pc Qt6Widgets "Qt6Core Qt6Gui" QT_WIDGETS_LIB
fi

for module in "${required_pkg_modules[@]}"; do
  module_version="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$selected_pc_dir" pkg-config --modversion "$module")"
  if [[ "$module_version" != "$expected_version" ]]; then
    echo "$module pkg-config version is $module_version, expected $expected_version" >&2
    exit 1
  fi
done

widget_cflags="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$selected_pc_dir" pkg-config --cflags Qt6Widgets)"
widget_libs="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$selected_pc_dir" pkg-config --libs Qt6Widgets)"
case " $widget_cflags $widget_libs " in
  *"$qt_root"*) ;;
  *)
    echo "Qt6Widgets pkg-config flags do not reference QT_ROOT: $qt_root" >&2
    exit 1
    ;;
esac

echo "QT_PKG_CONFIG_DIR=$selected_pc_dir"
echo "QT_PKG_CONFIG_SOURCE=$pc_source"
echo "QT_VERSION=$qt_version"
