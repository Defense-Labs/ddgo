#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "skipping universal Mach-O merge regression: requires macOS clang and lipo"
  exit 0
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
source "$script_dir/merge-macos-universal.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

for tool in clang lipo cmp stat; do
  command -v "$tool" >/dev/null || fail "required tool is unavailable: $tool"
done

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT

work_dir="$tmpdir/work"
mkdir -p "$work_dir"

printf '%s\n' 'int fixture_arm(void) { return 64; }' > "$tmpdir/arm.c"
printf '%s\n' 'int fixture_x86(void) { return 86; }' > "$tmpdir/x86.c"

arm_thin="$tmpdir/fixture-arm64.dylib"
amd_thin="$tmpdir/fixture-x86_64.dylib"
clang -arch arm64 -mmacosx-version-min=15.0 -dynamiclib "$tmpdir/arm.c" -o "$arm_thin"
clang -arch x86_64 -mmacosx-version-min=15.0 -dynamiclib "$tmpdir/x86.c" -o "$amd_thin"
chmod 751 "$arm_thin"

universal="$tmpdir/fixture-universal.dylib"
lipo -create "$arm_thin" "$amd_thin" -output "$universal"

assert_exact_universal() {
  local file="$1"
  local archs
  archs="$(lipo -archs "$file")"
  has_exact_universal_archs "$archs" || fail "expected exactly arm64 and x86_64: $file ($archs)"
}

assert_slice_matches() {
  local universal_file="$1"
  local arch="$2"
  local expected="$3"
  local extracted="$tmpdir/extracted-${arch}-$(basename "$universal_file")"
  lipo -thin "$arch" "$universal_file" -output "$extracted"
  cmp -s "$extracted" "$expected" || fail "$arch slice does not match its source: $universal_file"
}

assert_no_temporary_files() {
  if find "$work_dir" -type f -print -quit | grep -q .; then
    fail "merge left temporary Mach-O files in $work_dir"
  fi
}

test_successful_merge() {
  local name="$1"
  local arm_input="$2"
  local amd_input="$3"
  local output="$tmpdir/${name}.dylib"
  local mode
  local expected_mode

  merge_macho_slices "$arm_input" "$amd_input" "$output" "$name" "$work_dir"
  assert_exact_universal "$output"
  assert_slice_matches "$output" arm64 "$arm_thin"
  assert_slice_matches "$output" x86_64 "$amd_thin"
  mode="$(stat -f '%Lp' "$output")"
  expected_mode="$(stat -f '%Lp' "$arm_input")"
  [[ "$mode" == "$expected_mode" ]] || fail "merge did not preserve arm64 file mode: $name ($mode, expected $expected_mode)"
  assert_no_temporary_files
}

test_successful_merge thin-thin "$arm_thin" "$amd_thin"
test_successful_merge universal-universal "$universal" "$universal"
test_successful_merge thin-universal "$arm_thin" "$universal"
test_successful_merge universal-thin "$universal" "$amd_thin"

expect_merge_failure() {
  local name="$1"
  local arm_input="$2"
  local amd_input="$3"
  local output="$tmpdir/${name}.dylib"

  if merge_macho_slices "$arm_input" "$amd_input" "$output" "$name" "$work_dir"; then
    fail "merge unexpectedly succeeded: $name"
  fi
  [[ ! -e "$output" ]] || fail "failed merge wrote an output: $name"
  assert_no_temporary_files
}

invalid="$tmpdir/not-macho"
printf '%s\n' 'not a Mach-O file' > "$invalid"
expect_merge_failure missing-arm64 "$amd_thin" "$amd_thin"
expect_merge_failure missing-x86_64 "$arm_thin" "$arm_thin"
expect_merge_failure invalid-arm64 "$invalid" "$amd_thin"

echo "macOS universal Mach-O merge regression checks passed"
