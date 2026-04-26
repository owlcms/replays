#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LOCAL_FFMPEG_DIR="$REPO_ROOT/video_config/ffmpeg"
LOCAL_REPLAYS_CONFIG_DIR="$REPO_ROOT/video_config/replays"

show_usage() {
  cat <<'EOF'
Usage: scripts/replays.sh [--default-install|--installed] [--help] [-- <replays args...>]

Script options:
  --default-install, --installed  Use the latest installed owlcms-replays config directory
  -h, --help                      Show this script help

All other arguments are passed through to the replays binary.
EOF
}

show_usage_error() {
  local message="$1"
  echo "$message" >&2
  echo >&2
  show_usage >&2
}

find_latest_installed_version_dir() {
  local install_root="$1"
  local child
  local base
  local latest
  local versions=()

  [[ -d "$install_root" ]] || return 1

  for child in "$install_root"/*; do
    [[ -d "$child" ]] || continue
    base="$(basename "$child")"
    if [[ "$base" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.+-]+)?$ ]]; then
      versions+=("$base")
    fi
  done

  [[ ${#versions[@]} -gt 0 ]] || return 1

  latest="$(printf '%s\n' "${versions[@]}" | sort -V | tail -n 1)"
  [[ -n "$latest" ]] || return 1

  printf '%s\n' "$install_root/$latest"
}

USE_DEFAULT_INSTALL=false
BINARY_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --default-install|--installed)
      USE_DEFAULT_INSTALL=true
      shift
      ;;
    -h|--help)
      show_usage
      exit 0
      ;;
    --)
      shift
      while [[ $# -gt 0 ]]; do
        BINARY_ARGS+=("$1")
        shift
      done
      break
      ;;
    *)
      BINARY_ARGS+=("$1")
      shift
      ;;
  esac
done

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
    BINARY="$REPO_ROOT/replays.exe"
    CONTROL_PANEL_DIR="${VIDEO_CONTROLPANEL_DIR:-${APPDATA:-$HOME/AppData/Roaming}/owlcms-controlpanel}"
    REPLAYS_INSTALL_ROOT="${APPDATA:-$HOME/AppData/Roaming}/owlcms-replays"

    if [[ -n "${VIDEO_CONFIGDIR:-}" ]]; then
      FFMPEG_CONFIG_DIR="$VIDEO_CONFIGDIR"
    elif [[ -d "$CONTROL_PANEL_DIR/video_config/ffmpeg" ]]; then
      FFMPEG_CONFIG_DIR="$CONTROL_PANEL_DIR/video_config/ffmpeg"
    else
      FFMPEG_CONFIG_DIR="$LOCAL_FFMPEG_DIR"
    fi

    if [[ -n "${VIDEO_FFMPEG_PATH:-}" ]]; then
      FFMPEG_RUNTIME_DIR="$(dirname "$VIDEO_FFMPEG_PATH")"
    elif [[ -d "$CONTROL_PANEL_DIR/ffmpeg" ]]; then
      FFMPEG_RUNTIME_DIR="$CONTROL_PANEL_DIR/ffmpeg"
    else
      FFMPEG_RUNTIME_DIR="$FFMPEG_CONFIG_DIR"
    fi

    FFMPEG_EXECUTABLE="ffmpeg.exe"
    EXPORT_FFMPEG_PATH=true
    ;;
  *)
    if [[ -x "$REPO_ROOT/replays.exe" ]]; then
      BINARY="$REPO_ROOT/replays.exe"
    else
      BINARY="$REPO_ROOT/replays"
    fi

    CONTROL_PANEL_DIR="${VIDEO_CONTROLPANEL_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/owlcms-controlpanel}"
    REPLAYS_INSTALL_ROOT="${XDG_DATA_HOME:-$HOME/.local/share}/owlcms-replays"
    if [[ -d "$CONTROL_PANEL_DIR/ffmpeg" ]]; then
      FFMPEG_RUNTIME_DIR="$CONTROL_PANEL_DIR/ffmpeg"
    else
      FFMPEG_RUNTIME_DIR="$LOCAL_FFMPEG_DIR"
    fi
    if [[ -n "${VIDEO_CONFIGDIR:-}" ]]; then
      FFMPEG_CONFIG_DIR="$VIDEO_CONFIGDIR"
    elif [[ -d "$CONTROL_PANEL_DIR/video_config/ffmpeg" ]]; then
      FFMPEG_CONFIG_DIR="$CONTROL_PANEL_DIR/video_config/ffmpeg"
    else
      FFMPEG_CONFIG_DIR="$LOCAL_FFMPEG_DIR"
    fi
    FFMPEG_EXECUTABLE="ffmpeg"
    EXPORT_FFMPEG_PATH=true
    ;;
esac

if [[ ! -f "$BINARY" ]]; then
  show_usage_error "Built replays binary not found: $BINARY
Build it first, for example: go build -o replays.exe ./cmd/replays"
  exit 1
fi

REPLAYS_CONFIG_DIR="$LOCAL_REPLAYS_CONFIG_DIR"
if [[ "$USE_DEFAULT_INSTALL" == true ]]; then
  if ! REPLAYS_CONFIG_DIR="$(find_latest_installed_version_dir "$REPLAYS_INSTALL_ROOT")"; then
    show_usage_error "Installed replays config directory not found under: $REPLAYS_INSTALL_ROOT"
    exit 1
  fi
fi

if [[ ! -d "$FFMPEG_CONFIG_DIR" ]]; then
  show_usage_error "FFmpeg config directory not found: $FFMPEG_CONFIG_DIR"
  exit 1
fi

export VIDEO_CONFIGDIR="$FFMPEG_CONFIG_DIR"

if [[ "$EXPORT_FFMPEG_PATH" == true ]]; then
  if ! FFMPEG_PATH="$(find_ffmpeg_executable "$FFMPEG_RUNTIME_DIR" "$FFMPEG_EXECUTABLE")"; then
    show_usage_error "FFmpeg executable not found under: $FFMPEG_RUNTIME_DIR"
    exit 1
  fi

  export VIDEO_FFMPEG_PATH="$FFMPEG_PATH"
fi

exec "$BINARY" --configDir "$REPLAYS_CONFIG_DIR" "${BINARY_ARGS[@]}"