#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
package_dir="$repo_root/cmd/cameras"

cd "$package_dir"
go run fyne.io/tools/cmd/fyne@latest package \
  -os darwin \
  -name Cameras \
  -appID app.owlcms.cameras \
  -icon "$repo_root/internal/assets/Icon.png"

printf 'Created %s\n' "$package_dir/Cameras.app"