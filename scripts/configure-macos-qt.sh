#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

qt_framework_name() {
  local module="$1"
  case "$module" in
    Qt6?*) printf 'Qt%s\n' "${module#Qt6}" ;;
    *)
      echo "ERROR: cannot derive a macOS Qt framework name from module $module" >&2
      return 1
      ;;
  esac
}

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

if [[ ! -d "$qt_root" ]]; then
  fail "Qt root does not exist or is not a directory: $qt_root"
fi
if ! qt_root="$(cd "$qt_root" && pwd -P)"; then
  fail "cannot resolve Qt root: $qt_root"
fi
case "$qt_root" in
  /opt/homebrew/*|/usr/local/*)
    echo "QT_ROOT must be the pinned Qt distribution, not Homebrew: $qt_root" >&2
    exit 1
    ;;
esac

qmake="$qt_root/bin/qmake"
macdeployqt="$qt_root/bin/macdeployqt"
if [[ ! -x "$qmake" ]]; then
  fail "missing executable qmake: $qmake"
fi
if [[ ! -x "$macdeployqt" ]]; then
  fail "missing executable macdeployqt: $macdeployqt"
fi

qmake_query() {
  local key="$1"
  local value
  if ! value="$("$qmake" -query "$key")"; then
    fail "qmake query failed for $key: $qmake"
  fi
  if [[ -z "$value" ]]; then
    fail "qmake returned an empty value for $key: $qmake"
  fi
  printf '%s\n' "$value"
}

qt_version="$(qmake_query QT_VERSION)"
if [[ "$qt_version" != "$expected_version" ]]; then
  fail "Qt version is $qt_version, expected $expected_version ($qmake)"
fi

qt_lib_dir="$(qmake_query QT_INSTALL_LIBS)"
qt_include_dir="$(qmake_query QT_INSTALL_HEADERS)"
qt_archdata_dir="$(qmake_query QT_INSTALL_ARCHDATA)"
if [[ ! -d "$qt_lib_dir" ]]; then
  fail "missing Qt library directory reported by qmake: $qt_lib_dir"
fi
if [[ ! -d "$qt_include_dir" ]]; then
  fail "missing Qt include directory reported by qmake: $qt_include_dir"
fi
if [[ ! -d "$qt_archdata_dir/mkspecs" ]]; then
  fail "missing Qt mkspecs directory reported by qmake: $qt_archdata_dir/mkspecs"
fi

required_pkg_modules=(Qt6Platform Qt6Core Qt6Gui Qt6Widgets)
required_framework_modules=(Qt6Core Qt6Gui Qt6Widgets)
for module in "${required_framework_modules[@]}"; do
  framework="$(qt_framework_name "$module")" || fail "invalid Qt framework module: $module"
  headers="$qt_lib_dir/$framework.framework/Headers"
  framework_binary="$qt_lib_dir/$framework.framework/$framework"
  if [[ ! -d "$headers" ]]; then
    fail "missing required framework headers for $module ($framework): $headers"
  fi
  if [[ ! -e "$framework_binary" ]]; then
    fail "missing required framework binary for $module ($framework): $framework_binary"
  fi
done

# Qt's official macOS packages do not guarantee pkg-config metadata. Prefer
# suitable metadata when present; otherwise create only the Qt base-module
# records MIQT v0.15.0 needs. The records derive every path and version from
# qmake, so they cannot point at a separately installed Homebrew Qt.
official_pc_dir="$qt_lib_dir/pkgconfig"
use_official=1
official_failure=""
for module in "${required_pkg_modules[@]}"; do
  if [[ ! -f "$official_pc_dir/$module.pc" ]]; then
    use_official=0
    official_failure="missing $official_pc_dir/$module.pc"
    break
  fi
done

if [[ $use_official -eq 1 ]]; then
  for module in "${required_pkg_modules[@]}"; do
    if ! module_version="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$official_pc_dir" pkg-config --modversion "$module")" ||
      ! module_prefix="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$official_pc_dir" pkg-config --variable=prefix "$module")"; then
      use_official=0
      official_failure="invalid pkg-config metadata for $module in $official_pc_dir"
      break
    fi
    if [[ "$module_version" != "$expected_version" || "$module_prefix" != "$qt_root" ]]; then
      use_official=0
      official_failure="unexpected pkg-config metadata for $module in $official_pc_dir (version=$module_version prefix=$module_prefix)"
      break
    fi
  done
fi

prepare_generated_pc_dir() {
  if ! rm -rf "$pkgconfig_dir"; then
    fail "cannot remove generated pkg-config directory: $pkgconfig_dir"
  fi
  if ! mkdir -p "$pkgconfig_dir"; then
    fail "cannot create generated pkg-config directory: $pkgconfig_dir"
  fi
  if ! selected_pc_dir="$(cd "$pkgconfig_dir" && pwd -P)"; then
    fail "cannot resolve generated pkg-config directory: $pkgconfig_dir"
  fi
}

augment_official_framework_pc() {
  local module="$1"
  local framework
  local source_pc="$official_pc_dir/$module.pc"
  local output_pc="$selected_pc_dir/$module.pc"
  local temporary_pc="$output_pc.tmp"
  local extra_cflags

  framework="$(qt_framework_name "$module")" || return 1
  extra_cflags="-I$qt_lib_dir/$framework.framework/Headers -I$qt_lib_dir/$framework.framework -F$qt_lib_dir"
  if ! awk -v extra_cflags="$extra_cflags" '
    /^Cflags:[[:space:]]*/ {
      print "Cflags: " extra_cflags " " substr($0, index($0, ":") + 1)
      found_cflags = 1
      next
    }
    { print }
    END { if (!found_cflags) exit 1 }
  ' "$source_pc" > "$temporary_pc"; then
    rm -f "$temporary_pc"
    echo "ERROR: official pkg-config metadata has no usable Cflags entry for $module: $source_pc" >&2
    return 1
  fi
  if ! mv "$temporary_pc" "$output_pc"; then
    rm -f "$temporary_pc"
    echo "ERROR: cannot write Qt framework header overlay for $module: $output_pc" >&2
    return 1
  fi
}

if [[ $use_official -eq 1 ]]; then
  # Keep Qt's supplied module records and transitive dependency/linker flags.
  # The overlay puts framework headers before module-local forwarding paths so
  # MIQT can resolve both <qfoo.h> and <QtCore/qfoo.h> forms.
  prepare_generated_pc_dir
  if ! cp "$official_pc_dir"/*.pc "$selected_pc_dir/"; then
    fail "cannot copy official Qt pkg-config metadata into overlay: $official_pc_dir"
  fi
  for module in "${required_framework_modules[@]}"; do
    augment_official_framework_pc "$module" || fail "cannot add framework header paths to official metadata for $module"
  done
  pc_source="official-overlay"
else
  echo "Qt pkg-config metadata is unavailable or incomplete: $official_failure; generating fallback metadata" >&2
  prepare_generated_pc_dir
  pc_source="generated"

  if ! (
    cat > "$selected_pc_dir/Qt6Platform.pc" <<EOF
prefix=$qt_root
libdir=$qt_lib_dir
includedir=$qt_include_dir
mkspecsdir=$qt_archdata_dir/mkspecs

Name: Qt6 Platform
Description: Qt 6 macOS platform settings derived from qmake
Version: $qt_version
Cflags: -I\${mkspecsdir}/macx-clang
EOF
  ); then
    fail "failed to generate fallback pkg-config metadata: $selected_pc_dir/Qt6Platform.pc"
  fi

  write_framework_pc() {
    local module="$1"
    local requires="$2"
    local define="$3"
    local framework
    local pc_file="$selected_pc_dir/$module.pc"
    framework="$(qt_framework_name "$module")" || return 1
    if ! (
      cat > "$pc_file" <<EOF
prefix=$qt_root
libdir=$qt_lib_dir
includedir=$qt_include_dir

Name: $module
Description: Qt 6 $framework framework derived from qmake
Version: $qt_version
Libs: -F\${libdir} -framework $framework
Cflags: -I\${libdir}/$framework.framework/Headers -I\${libdir}/$framework.framework -F\${libdir} -D$define
Requires: $requires
EOF
    ); then
      echo "ERROR: failed to generate fallback pkg-config metadata for $module: $pc_file" >&2
      return 1
    fi
  }

  write_framework_pc Qt6Core Qt6Platform QT_CORE_LIB || fail "cannot generate fallback metadata for Qt6Core"
  write_framework_pc Qt6Gui Qt6Core QT_GUI_LIB || fail "cannot generate fallback metadata for Qt6Gui"
  write_framework_pc Qt6Widgets "Qt6Core Qt6Gui" QT_WIDGETS_LIB || fail "cannot generate fallback metadata for Qt6Widgets"
fi

for module in "${required_pkg_modules[@]}"; do
  if ! module_version="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$selected_pc_dir" pkg-config --modversion "$module")"; then
    fail "invalid or incomplete pkg-config metadata for $module in $selected_pc_dir"
  fi
  if [[ "$module_version" != "$expected_version" ]]; then
    fail "$module pkg-config version is $module_version, expected $expected_version ($selected_pc_dir)"
  fi
done

if ! widget_cflags="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$selected_pc_dir" pkg-config --cflags Qt6Widgets)"; then
  fail "cannot read Qt6Widgets compiler flags from pkg-config metadata: $selected_pc_dir"
fi
if ! widget_libs="$(PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR="$selected_pc_dir" pkg-config --libs Qt6Widgets)"; then
  fail "cannot read Qt6Widgets linker flags from pkg-config metadata: $selected_pc_dir"
fi
case " $widget_cflags $widget_libs " in
  *"$qt_root"*) ;;
  *)
    fail "Qt6Widgets pkg-config flags do not reference QT_ROOT: $qt_root"
    ;;
esac

echo "QT_PKG_CONFIG_DIR=$selected_pc_dir"
echo "QT_PKG_CONFIG_SOURCE=$pc_source"
echo "QT_VERSION=$qt_version"
