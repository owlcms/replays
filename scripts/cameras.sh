#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LOCAL_FFMPEG_DIR="$REPO_ROOT/video_config/ffmpeg"

find_ffmpeg_executable() {
  local root="$1"
  local executable_name="$2"
  local candidate

  candidate="$root/bin/$executable_name"
  if [[ -x "$candidate" ]]; then
    printf '%s\n' "$candidate"
    return 0
  fi

  candidate="$root/$executable_name"
  if [[ -x "$candidate" ]]; then
    printf '%s\n' "$candidate"
    return 0
  fi

  local child
  for child in "$root"/*; do
    [[ -d "$child" ]] || continue

    candidate="$child/bin/$executable_name"
    if [[ -x "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi

    candidate="$child/$executable_name"
    if [[ -x "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done

  return 1
}

case "${OS:-}" in
  Windows_NT)
    BINARY="$REPO_ROOT/cameras.exe"
    FFMPEG_DIR="$LOCAL_FFMPEG_DIR"
    FFMPEG_EXECUTABLE="ffmpeg.exe"
    EXPORT_FFMPEG_PATH=true
    ;;
  *)
    if [[ -x "$REPO_ROOT/cameras.exe" ]]; then
      BINARY="$REPO_ROOT/cameras.exe"
    else
      BINARY="$REPO_ROOT/cameras"
    fi

    CONTROL_PANEL_DIR="${VIDEO_CONTROLPANEL_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/owlcms-controlpanel}"
    if [[ -d "$CONTROL_PANEL_DIR/ffmpeg" ]]; then
      FFMPEG_DIR="$CONTROL_PANEL_DIR/ffmpeg"
    else
      FFMPEG_DIR="$LOCAL_FFMPEG_DIR"
    fi
    FFMPEG_EXECUTABLE="ffmpeg"
    EXPORT_FFMPEG_PATH=true
    ;;
esac

if [[ ! -f "$BINARY" ]]; then
  echo "Built cameras binary not found: $BINARY" >&2
  echo "Build it first, for example: go build -o cameras.exe ./cmd/cameras" >&2
  exit 1
fi

if [[ ! -d "$FFMPEG_DIR" ]]; then
  echo "FFmpeg config directory not found: $FFMPEG_DIR" >&2
  exit 1
fi

export VIDEO_CONFIGDIR="$FFMPEG_DIR"

if [[ "$EXPORT_FFMPEG_PATH" == true ]]; then
  if ! FFMPEG_PATH="$(find_ffmpeg_executable "$FFMPEG_DIR" "$FFMPEG_EXECUTABLE")"; then
    echo "FFmpeg executable not found under: $FFMPEG_DIR" >&2
    exit 1
  fi

  export VIDEO_FFMPEG_PATH="$FFMPEG_PATH"
fi

exec "$BINARY" --configDir "$REPO_ROOT/video_config/cameras" "$@"