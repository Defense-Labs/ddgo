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

for tool in clang lipo cmp stat codesign ditto; do
  command -v "$tool" >/dev/null || fail "required tool is unavailable: $tool"
done
[[ -x /usr/libexec/PlistBuddy ]] || fail "required tool is unavailable: /usr/libexec/PlistBuddy"

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

# Exercise the complete merger in a separate shell. In particular, this makes
# the merger's EXIT trap run at the same point as it does in CD.
printf '%s\n' 'int main(void) { return 64; }' > "$tmpdir/arm-main.c"
printf '%s\n' 'int main(void) { return 86; }' > "$tmpdir/x86-main.c"
arm_executable="$tmpdir/ddgo-arm64"
amd_executable="$tmpdir/ddgo-x86_64"
clang -arch arm64 -mmacosx-version-min=15.0 "$tmpdir/arm-main.c" -o "$arm_executable"
clang -arch x86_64 -mmacosx-version-min=15.0 "$tmpdir/x86-main.c" -o "$amd_executable"

create_app_fixture() {
  local app="$1"
  local executable="$2"
  mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
  cp -p "$executable" "$app/Contents/MacOS/ddgo"
  printf '%s\n' \
    '<?xml version="1.0" encoding="UTF-8"?>' \
    '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
    '<plist version="1.0"><dict>' \
    '<key>CFBundleExecutable</key><string>ddgo</string>' \
    '<key>CFBundleIdentifier</key><string>org.ddgo.universal-merge-test</string>' \
    '<key>CFBundlePackageType</key><string>APPL</string>' \
    '<key>LSMinimumSystemVersion</key><string>15.0</string>' \
    '</dict></plist>' > "$app/Contents/Info.plist"
  printf '%s\n' 'universal merge fixture' > "$app/Contents/Resources/fixture.txt"
}

assert_no_full_merge_temporary_directories() {
  local temporary_root="$1"
  if find "$temporary_root" -mindepth 1 -print -quit | grep -q .; then
    fail "complete merger left temporary directories below $temporary_root"
  fi
}

arm_app="$tmpdir/full-arm64/DDGo.app"
amd_app="$tmpdir/full-x86_64/DDGo.app"
full_output="$tmpdir/full-output/DDGo.app"
full_merge_tmp="$tmpdir/full-merge-tmp"
mkdir -p "$full_merge_tmp"
create_app_fixture "$arm_app" "$arm_executable"
create_app_fixture "$amd_app" "$amd_executable"

if ! TMPDIR="$full_merge_tmp" bash "$script_dir/merge-macos-universal.sh" \
  "$arm_app" "$amd_app" "$full_output" > "$tmpdir/full-merge-success.log" 2>&1; then
  cat "$tmpdir/full-merge-success.log" >&2
  fail "complete universal merger failed"
fi
[[ -x "$full_output/Contents/MacOS/ddgo" ]] || fail "complete merger did not create an executable"
assert_exact_universal "$full_output/Contents/MacOS/ddgo"
assert_slice_matches "$full_output/Contents/MacOS/ddgo" arm64 "$arm_executable"
assert_slice_matches "$full_output/Contents/MacOS/ddgo" x86_64 "$amd_executable"
codesign --verify --deep --strict --verbose=2 "$full_output"
if grep -Fq 'tmpdir: unbound variable' "$tmpdir/full-merge-success.log"; then
  fail "complete merger reported an unbound cleanup variable"
fi
assert_no_full_merge_temporary_directories "$full_merge_tmp"

failure_output="$tmpdir/failure-output/DDGo.app"
failure_merge_tmp="$tmpdir/failure-merge-tmp"
mkdir -p "$failure_merge_tmp"
if TMPDIR="$failure_merge_tmp" bash "$script_dir/merge-macos-universal.sh" \
  "$amd_app" "$amd_app" "$failure_output" > "$tmpdir/full-merge-failure.log" 2>&1; then
  fail "complete merger accepted an arm64 input without arm64"
fi
grep -Fq 'source lacks required arm64 architecture' "$tmpdir/full-merge-failure.log" ||
  fail "complete merger did not report the original architecture failure"
if grep -Fq 'tmpdir: unbound variable' "$tmpdir/full-merge-failure.log"; then
  fail "complete merger obscured failure with an unbound cleanup variable"
fi
assert_no_full_merge_temporary_directories "$failure_merge_tmp"

echo "macOS universal Mach-O merge regression checks passed"
