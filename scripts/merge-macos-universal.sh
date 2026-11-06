#!/usr/bin/env bash
set -euo pipefail

has_exact_arch() {
  local archs="$1"
  local wanted="$2"
  local arch
  for arch in $archs; do
    [[ "$arch" == "$wanted" ]] && return 0
  done
  return 1
}

has_exact_universal_archs() {
  local archs="$1"
  local count=0
  local arch
  has_exact_arch "$archs" arm64 || return 1
  has_exact_arch "$archs" x86_64 || return 1
  for arch in $archs; do
    count=$((count + 1))
  done
  [[ $count -eq 2 ]]
}

# Writes exactly one requested architecture to output. lipo -create cannot
# select an architecture from a fat input with -arch, so universal Qt binaries
# must be thinned before they are combined with the architecture-specific app.
prepare_thin_macho() {
  local input="$1"
  local wanted_arch="$2"
  local output="$3"
  local relative_path="$4"
  local archs
  local output_archs

  if ! archs="$(lipo -archs "$input" 2>/dev/null)"; then
    echo "cannot inspect Mach-O architectures: $relative_path" >&2
    return 1
  fi
  if ! has_exact_arch "$archs" "$wanted_arch"; then
    echo "source lacks required $wanted_arch architecture: $relative_path ($archs)" >&2
    return 1
  fi

  rm -f "$output"
  if [[ "$archs" == "$wanted_arch" ]]; then
    cp -p "$input" "$output"
  elif ! lipo -thin "$wanted_arch" "$input" -output "$output"; then
    echo "cannot extract $wanted_arch architecture: $relative_path ($archs)" >&2
    return 1
  fi

  output_archs="$(lipo -archs "$output" 2>/dev/null || true)"
  if [[ "$output_archs" != "$wanted_arch" ]]; then
    echo "architecture extraction produced an invalid result: $relative_path ($output_archs)" >&2
    return 1
  fi
}

merge_macho_slices() {
  local arm_file="$1"
  local amd_file="$2"
  local out_file="$3"
  local relative_path="$4"
  local work_dir="$5"
  local thin_arm
  local thin_amd
  local merged_file
  local merged_archs
  local mode

  thin_arm="$(mktemp "$work_dir/arm64.XXXXXX")"
  thin_amd="$(mktemp "$work_dir/x86_64.XXXXXX")"
  merged_file="$(mktemp "$work_dir/merged.XXXXXX")"

  if ! prepare_thin_macho "$arm_file" arm64 "$thin_arm" "$relative_path"; then
    rm -f "$thin_arm" "$thin_amd" "$merged_file"
    return 1
  fi
  if ! prepare_thin_macho "$amd_file" x86_64 "$thin_amd" "$relative_path"; then
    rm -f "$thin_arm" "$thin_amd" "$merged_file"
    return 1
  fi
  if ! lipo -create "$thin_arm" "$thin_amd" -output "$merged_file"; then
    echo "cannot merge Mach-O architectures: $relative_path" >&2
    rm -f "$thin_arm" "$thin_amd" "$merged_file"
    return 1
  fi

  if ! merged_archs="$(lipo -archs "$merged_file" 2>/dev/null)" || ! has_exact_universal_archs "$merged_archs"; then
    echo "merged Mach-O has invalid architectures: $relative_path (${merged_archs:-unknown})" >&2
    rm -f "$thin_arm" "$thin_amd" "$merged_file"
    return 1
  fi
  mode="$(stat -f '%Lp' "$arm_file")"
  chmod "$mode" "$merged_file"
  mv -f "$merged_file" "$out_file"
  rm -f "$thin_arm" "$thin_amd"
}

merge_universal_app() (
  if [[ $# -ne 3 ]]; then
    echo "usage: $0 <arm64-app> <amd64-app> <out-app>" >&2
    return 1
  fi

  local arm_app="$1"
  local amd_app="$2"
  local out_app="$3"
  local app
  local tmpdir
  local rel
  local arm_file
  local amd_file
  local out_file
  local arm_exists
  local amd_exists
  local arm_is_macho
  local amd_is_macho
  local merged
  local skipped
  local warnings

  for app in "$arm_app" "$amd_app"; do
  if [[ ! -d "$app" ]]; then
    echo "app bundle not found: $app" >&2
    exit 1
  fi
  if [[ ! -f "$app/Contents/Info.plist" ]]; then
    echo "missing Info.plist: $app/Contents/Info.plist" >&2
    exit 1
  fi
  if [[ ! -f "$app/Contents/MacOS/ddgo" ]]; then
    echo "missing executable: $app/Contents/MacOS/ddgo" >&2
    exit 1
  fi
  done

  rm -rf "$out_app"
  mkdir -p "$(dirname "$out_app")"
  ditto "$arm_app" "$out_app"

  tmpdir="$(mktemp -d)"
  cleanup() {
    local status=$?
    if ! rm -rf "$tmpdir"; then
      echo "failed to remove universal merge temporary directory: $tmpdir" >&2
      return 1
    fi
    return "$status"
  }
  trap cleanup EXIT

  (
    cd "$arm_app"
    find . -type f -print | sort
  ) > "$tmpdir/arm-files.txt"

  (
    cd "$amd_app"
    find . -type f -print | sort
  ) > "$tmpdir/amd-files.txt"

  cat "$tmpdir/arm-files.txt" "$tmpdir/amd-files.txt" | sort -u > "$tmpdir/all-files.txt"

  merged=0
  skipped=0
  warnings=0

  while IFS= read -r rel; do
  [[ -z "$rel" ]] && continue
  arm_file="$arm_app/$rel"
  amd_file="$amd_app/$rel"
  out_file="$out_app/$rel"

  arm_exists=0
  amd_exists=0
  [[ -f "$arm_file" ]] && arm_exists=1
  [[ -f "$amd_file" ]] && amd_exists=1

  arm_is_macho=0
  amd_is_macho=0
  if [[ $arm_exists -eq 1 ]] && file "$arm_file" | grep -q 'Mach-O'; then arm_is_macho=1; fi
  if [[ $amd_exists -eq 1 ]] && file "$amd_file" | grep -q 'Mach-O'; then amd_is_macho=1; fi

  if [[ $arm_is_macho -eq 1 || $amd_is_macho -eq 1 ]]; then
    if [[ $arm_exists -ne 1 || $amd_exists -ne 1 ]]; then
      echo "Mach-O file exists only in one slice; cannot create universal app:" >&2
      echo "$rel" >&2
      exit 1
    fi

    merge_macho_slices "$arm_file" "$amd_file" "$out_file" "$rel" "$tmpdir"
    merged=$((merged + 1))
    continue
  fi

  if [[ $arm_exists -eq 1 && $amd_exists -eq 1 ]]; then
    if ! cmp -s "$arm_file" "$amd_file"; then
      case "$rel" in
        ./Contents/Info.plist|./Contents/Resources/qt.conf)
          echo "warning: non-Mach-O metadata differs across slices; preferring arm64 copy: $rel" >&2
          warnings=$((warnings + 1))
          ;;
      esac
    fi
  fi
  skipped=$((skipped + 1))
  done < "$tmpdir/all-files.txt"

  /usr/libexec/PlistBuddy -c 'Set :LSMinimumSystemVersion 15.0' "$out_app/Contents/Info.plist"

  codesign --force --deep --sign - "$out_app"
  codesign --verify --deep --verbose=2 "$out_app"

  echo "merged Mach-O files: $merged"
  echo "skipped non-Mach-O files: $skipped"
  echo "warnings: $warnings"
)

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  merge_universal_app "$@"
fi
