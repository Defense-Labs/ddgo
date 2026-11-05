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

macos_list_load_commands() {
  local file="$1"
  otool -l "$file" | awk '
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
  otool -l "$file" | awk '
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
  otool -l "$file" | awk '
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
        done < <(macos_list_rpaths "$rpath_owner")
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
