#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <arm64-app> <amd64-app> <expected-qt-version>" >&2
  exit 1
fi

arm_app="$1"
amd_app="$2"
expected_qt_version="$3"
metadata_rel="Contents/Resources/ddgo-qt-build-metadata.txt"
max_reported=40

fail=0
reported=0

report() {
  if [[ $reported -lt $max_reported ]]; then
    echo "ERROR: $*" >&2
  fi
  reported=$((reported + 1))
  fail=$((fail + 1))
}

metadata_value() {
  local app="$1"
  local key="$2"
  awk -F= -v key="$key" '$1 == key { print substr($0, length(key) + 2); exit }' "$app/$metadata_rel"
}

list_macho_files() {
  local app="$1"
  (
    cd "$app"
    while IFS= read -r -d '' file; do
      if file "$file" | grep -q 'Mach-O'; then
        printf '%s\n' "${file#./}"
      fi
    done < <(find . -type f -print0)
  ) | sort
}

list_macho_dependency_inventory() {
  local file="$1"
  otool -l "$file" | awk '
    /cmd LC_ID_DYLIB|cmd LC_LOAD_DYLIB|cmd LC_LOAD_WEAK_DYLIB|cmd LC_REEXPORT_DYLIB|cmd LC_LOAD_UPWARD_DYLIB/ {
      in_load = 1
      next
    }
    in_load && /name / {
      print $2
      in_load = 0
      next
    }
    /cmd LC_/ { in_load = 0 }
  ' | sort -u
}

list_rpaths() {
  local file="$1"
  otool -l "$file" | awk '
    /cmd LC_RPATH/ { in_rpath = 1; next }
    in_rpath && /path / { print $2; in_rpath = 0; next }
    /cmd LC_/ { in_rpath = 0 }
  ' | sort -u
}

for app in "$arm_app" "$amd_app"; do
  if [[ ! -d "$app" ]]; then
    report "app bundle not found: $app"
    continue
  fi
  if [[ ! -f "$app/$metadata_rel" ]]; then
    report "missing Qt build metadata: $app/$metadata_rel"
  fi
done

if [[ $fail -ne 0 ]]; then
  exit 1
fi

for key in distribution aqtinstall_version qt_version; do
  arm_value="$(metadata_value "$arm_app" "$key")"
  amd_value="$(metadata_value "$amd_app" "$key")"
  if [[ -z "$arm_value" || -z "$amd_value" ]]; then
    report "missing $key in slice build metadata"
  elif [[ "$arm_value" != "$amd_value" ]]; then
    report "slice metadata mismatch for $key: arm64=$arm_value amd64=$amd_value"
  fi
done
if [[ "$(metadata_value "$arm_app" qt_version)" != "$expected_qt_version" ]]; then
  report "slice Qt version is not the pinned value $expected_qt_version"
fi

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT

list_macho_files "$arm_app" > "$tmpdir/arm-macho.txt"
list_macho_files "$amd_app" > "$tmpdir/amd-macho.txt"

while IFS= read -r rel; do
  [[ -z "$rel" ]] && continue
  if ! grep -Fqx "$rel" "$tmpdir/amd-macho.txt"; then
    report "Mach-O file exists only in arm64 slice: $rel"
  fi
done < "$tmpdir/arm-macho.txt"
while IFS= read -r rel; do
  [[ -z "$rel" ]] && continue
  if ! grep -Fqx "$rel" "$tmpdir/arm-macho.txt"; then
    report "Mach-O file exists only in amd64 slice: $rel"
  fi
done < "$tmpdir/amd-macho.txt"

while IFS= read -r rel; do
  [[ -z "$rel" ]] && continue
  arm_file="$arm_app/$rel"
  amd_file="$amd_app/$rel"
  [[ -f "$amd_file" ]] || continue

  arm_archs="$(lipo -archs "$arm_file")"
  amd_archs="$(lipo -archs "$amd_file")"
  [[ " $arm_archs " == *" arm64 "* ]] || report "arm64 slice lacks arm64: $rel ($arm_archs)"
  [[ " $amd_archs " == *" x86_64 "* ]] || report "amd64 slice lacks x86_64: $rel ($amd_archs)"

  list_macho_dependency_inventory "$arm_file" > "$tmpdir/arm-deps.txt"
  list_macho_dependency_inventory "$amd_file" > "$tmpdir/amd-deps.txt"
  if ! cmp -s "$tmpdir/arm-deps.txt" "$tmpdir/amd-deps.txt"; then
    report "Mach-O install-name/dependency inventory differs: $rel"
    if [[ $reported -le $max_reported ]]; then
      diff -u "$tmpdir/arm-deps.txt" "$tmpdir/amd-deps.txt" >&2 || true
    fi
  fi

  list_rpaths "$arm_file" > "$tmpdir/arm-rpaths.txt"
  list_rpaths "$amd_file" > "$tmpdir/amd-rpaths.txt"
  if ! cmp -s "$tmpdir/arm-rpaths.txt" "$tmpdir/amd-rpaths.txt"; then
    report "runtime-search-path inventory differs: $rel"
    if [[ $reported -le $max_reported ]]; then
      diff -u "$tmpdir/arm-rpaths.txt" "$tmpdir/amd-rpaths.txt" >&2 || true
    fi
  fi
done < "$tmpdir/arm-macho.txt"

if [[ $reported -gt $max_reported ]]; then
  echo "ERROR: $((reported - max_reported)) additional compatibility issue(s) omitted" >&2
fi
if [[ $fail -ne 0 ]]; then
  echo "macOS slice compatibility check failed with $fail issue(s)" >&2
  exit 1
fi

echo "macOS slices have matching pinned Qt metadata and Mach-O dependency inventories"
