#!/usr/bin/env bash

# Shared static Mach-O dependency helpers. Callers must set -euo pipefail and
# decide whether an unresolved weak dependency is a warning or a failure.

macos_has_arch() {
  local archs="$1"
  local wanted="$2"
  local arch
  for arch in $archs; do
    [[ "$arch" == "$wanted" ]] && return 0
  done
  return 1
}

macos_otool_load_commands() {
  local file="$1"
  local arch="${2:-}"
  if [[ -n "$arch" ]]; then
    otool -arch "$arch" -l "$file"
  else
    otool -l "$file"
  fi
}

macos_list_load_commands() {
  local file="$1"
  local arch="${2:-}"
  macos_otool_load_commands "$file" "$arch" | awk '
    /cmd LC_LOAD_DYLIB|cmd LC_LOAD_WEAK_DYLIB|cmd LC_REEXPORT_DYLIB|cmd LC_LOAD_UPWARD_DYLIB/ {
      command = $2
      want_name = 1
      next
    }
    want_name && /name / {
      print command "|" $2
      want_name = 0
      next
    }
    /cmd LC_/ { want_name = 0 }
  '
}

macos_list_dependency_inventory() {
  local file="$1"
  local arch="${2:-}"
  macos_otool_load_commands "$file" "$arch" | awk '
    /cmd LC_ID_DYLIB|cmd LC_LOAD_DYLIB|cmd LC_LOAD_WEAK_DYLIB|cmd LC_REEXPORT_DYLIB|cmd LC_LOAD_UPWARD_DYLIB/ {
      want_name = 1
      next
    }
    want_name && /name / {
      print $2
      want_name = 0
      next
    }
    /cmd LC_/ { want_name = 0 }
  ' | sort -u
}

macos_list_rpaths() {
  local file="$1"
  local arch="${2:-}"
  macos_otool_load_commands "$file" "$arch" | awk '
    /cmd LC_RPATH/ { want_path = 1; next }
    want_path && /path / { print $2; want_path = 0; next }
    /cmd LC_/ { want_path = 0 }
  '
}

macos_is_system_dependency() {
  case "$1" in
    /System/Library/*|/usr/lib/*)
      if [[ "$1" == *"/../"* || "$1" == */.. ]]; then
        return 1
      fi
      return 0
      ;;
  esac
  return 1
}

macos_is_homebrew_dependency() {
  case "$1" in
    /opt/homebrew/*|/usr/local/*) return 0 ;;
  esac
  return 1
}

macos_is_weak_load_command() {
  [[ "$1" == "LC_LOAD_WEAK_DYLIB" ]]
}

macos_has_rpath() {
  local file="$1"
  local wanted="$2"
  local arch="${3:-}"
  local rpath
  while IFS= read -r rpath; do
    [[ "$rpath" == "$wanted" ]] && return 0
  done < <(macos_list_rpaths "$file" "$arch")
  return 1
}

macos_add_rpath_if_missing() {
  local file="$1"
  local rpath="$2"
  local arch="${3:-}"
  if macos_has_rpath "$file" "$rpath" "$arch"; then
    echo "runtime path already present ($arch): $file -> $rpath"
    return 0
  fi
  echo "adding runtime path ($arch): $file -> $rpath"
  if ! install_name_tool -add_rpath "$rpath" "$file"; then
    echo "ERROR: cannot add runtime path to $file; Mach-O header padding may be insufficient: $rpath" >&2
    return 1
  fi
}

macos_realpath_existing_file() {
  local path="$1"
  [[ -f "$path" ]] || return 1
  python3 - "$path" <<'PY'
import os
import sys

path = os.path.realpath(sys.argv[1])
if not os.path.isfile(path):
    raise SystemExit(1)
print(path)
PY
}

macos_path_is_within_app() {
  local app="$1"
  local path="$2"
  local app_real
  app_real="$(cd "$app" && pwd -P)"
  [[ "$path" == "$app_real/"* ]]
}

macos_expand_dyld_token() {
  local value="$1"
  local loader_file="$2"
  local executable="$3"
  case "$value" in
    @loader_path/*) printf '%s/%s\n' "$(dirname "$loader_file")" "${value#@loader_path/}" ;;
    @executable_path/*) printf '%s/%s\n' "$(dirname "$executable")" "${value#@executable_path/}" ;;
    *) printf '%s\n' "$value" ;;
  esac
}

# macos_resolve_dependency prints a canonical bundled file path when a direct
# non-system dependency resolves from the loading binary's rpaths. It does not
# model dyld's complete loader-chain or runtime dlopen behavior.
macos_resolve_dependency() {
  local app="$1"
  local executable="$2"
  local loader_file="$3"
  local dependency="$4"
  local arch="${5:-}"
  local rpath
  local rpath_owner
  local candidate
  local resolved

  case "$dependency" in
    @loader_path/*|@executable_path/*)
      candidate="$(macos_expand_dyld_token "$dependency" "$loader_file" "$executable")"
      ;;
    @rpath/*)
      # dyld's run-path stack includes the loading binary's paths and, for a
      # dependent library or plugin, paths established by the main executable.
      # This intentionally remains a bounded static approximation rather than
      # a complete emulation of every dependency-chain search path.
      for rpath_owner in "$loader_file" "$executable"; do
        while IFS= read -r rpath; do
          [[ -z "$rpath" ]] && continue
          rpath="$(macos_expand_dyld_token "$rpath" "$rpath_owner" "$executable")"
          candidate="$rpath/${dependency#@rpath/}"
          if resolved="$(macos_realpath_existing_file "$candidate")" && macos_path_is_within_app "$app" "$resolved"; then
            printf '%s\n' "$resolved"
            return 0
          fi
        done < <(macos_list_rpaths "$rpath_owner" "$arch")
        [[ "$rpath_owner" == "$executable" ]] && break
      done
      return 1
      ;;
    /*)
      return 1
      ;;
    *)
      return 1
      ;;
  esac

  if resolved="$(macos_realpath_existing_file "$candidate")" && macos_path_is_within_app "$app" "$resolved"; then
    printf '%s\n' "$resolved"
    return 0
  fi
  return 1
}

# Returns success only if the packaged layout resolves every required direct
# non-system dependency for one architecture. It intentionally does not model
# runtime dlopen calls or every possible transitive dyld loader-chain path.
macos_verify_app_dependencies() {
  local app="$1"
  local executable="$2"
  local arch="$3"
  local file
  local load_command
  local dependency
  local rpaths
  local failed=0

  while IFS= read -r -d '' file; do
    if [[ ! -e "$file" ]]; then
      echo "ERROR: dangling bundled symlink in $app: $file" >&2
      failed=1
    fi
  done < <(find "$app/Contents" -type l -print0)

  while IFS= read -r -d '' file; do
    if ! file "$file" | grep -q 'Mach-O'; then
      continue
    fi
    while IFS='|' read -r load_command dependency; do
      [[ -z "$dependency" ]] && continue
      if macos_is_system_dependency "$dependency"; then
        continue
      fi
      if macos_is_homebrew_dependency "$dependency"; then
        echo "ERROR: Homebrew dependency in $file ($arch): $dependency" >&2
        failed=1
        continue
      fi
      if macos_resolve_dependency "$app" "$executable" "$file" "$dependency" "$arch" >/dev/null; then
        continue
      fi
      if macos_is_weak_load_command "$load_command"; then
        echo "WARN: unresolved weak dependency in $file ($arch): $dependency" >&2
        continue
      fi
      rpaths="$(macos_list_rpaths "$file" "$arch" | tr '\n' ' ')"
      echo "ERROR: unresolved required dependency in $file ($arch): $dependency" >&2
      echo "  LC_RPATH entries: ${rpaths:-<none>}" >&2
      echo "  expected bundled frameworks below: $app/Contents/Frameworks" >&2
      failed=1
    done < <(macos_list_load_commands "$file" "$arch")
  done < <(find "$app/Contents" -type f -print0)

  [[ $failed -eq 0 ]]
}
