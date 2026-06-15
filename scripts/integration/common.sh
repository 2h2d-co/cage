#!/usr/bin/env bash
set -euo pipefail

integration_config_default() {
  printf '%s/cage/integration-test/config.toml' "${XDG_CONFIG_HOME:-$HOME/.config}"
}

export CAGE_CONFIG="${CAGE_CONFIG:-$(integration_config_default)}"

cage_it() {
  CAGE_CONFIG="$CAGE_CONFIG" go run . "$@"
}

cage_it_with_tty_stdin() {
  if [[ ! -r /dev/tty ]]; then
    echo "error: /dev/tty is required for secure interactive input" >&2
    exit 1
  fi
  CAGE_CONFIG="$CAGE_CONFIG" go run . "$@" < /dev/tty
}

yes_args() {
  if [[ "${OVERRIDE:-false}" == "true" ]]; then
    printf '%s\n' --yes
  fi
}

force_slot_args() {
  if [[ "${OVERRIDE:-false}" == "true" ]]; then
    printf '%s\n' --force-slot
  fi
}

serial_args() {
  if [[ -n "${SERIAL:-}" ]]; then
    printf '%s\n%s\n' --serial "$SERIAL"
  fi
}

environment_uuid() {
  local name="$1"
  cage_it environment list | awk -v name="$name" '
    $1 == name {
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^uuid=/) {
          sub(/^uuid=/, "", $i)
          print $i
          exit
        }
      }
    }
  '
}

require_primary_uuid() {
  if [[ -z "${PRIMARY_UUID:-}" ]]; then
    PRIMARY_UUID="$(environment_uuid integration-test)"
  fi
  if [[ -z "${PRIMARY_UUID:-}" ]]; then
    echo "error: --primary-uuid is required because integration-test is not configured yet" >&2
    exit 1
  fi
}

create_provider_securely() {
  local provider="$1"
  local identity="$2"
  echo "Enter the 1Password service account token for provider '$provider'." >&2
  cage_it_with_tty_stdin provider 1p create "$provider" --identity "$identity" $(yes_args)
}
