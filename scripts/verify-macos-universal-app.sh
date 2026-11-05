#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
source "$script_dir/macos-macho-deps.sh"

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <app-path> <max-minos>" >&2
  exit 1
fi

app="$1"
max_minos="$2"

bash scripts/verify-macos-app.sh "$app" arm64 "$max_minos"
bash scripts/verify-macos-app.sh "$app" x86_64 "$max_minos"

fail=0
while IFS= read -r -d '' f; do
  if file "$f" | grep -q 'Mach-O'; then
    archs="$(lipo -archs "$f")"
    if ! macos_has_arch "$archs" arm64 || ! macos_has_arch "$archs" x86_64; then
      echo "missing universal arch pair: $f ($archs)" >&2
      fail=1
    fi
  fi
done < <(find "$app/Contents" -type f -print0)

if [[ $fail -ne 0 ]]; then
  exit 1
fi

echo "universal macOS app verification passed"
