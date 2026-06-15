#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/common.sh"

usage() {
  cat <<'EOF'
Usage: scripts/integration/setup-yubikey-touch.sh [--primary-uuid UUID] [--serial SERIAL] [--override]

Creates optional YubiKey touch-only integration config:
  identity:    integration-test-yk-touch
  provider:    integration-test-yk-touch
  environment: integration-test-yk-touch
  profile:     integration-test-yubikey-touch

Uses age-plugin-yubikey --slot 19, which maps to PIV retired slot 19 / hex slot 94.
Policies: --pin-policy never --touch-policy always.
EOF
}

OVERRIDE=false
PRIMARY_UUID=""
SERIAL=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --override|--force)
      OVERRIDE=true
      shift
      ;;
    --primary-uuid)
      if [[ $# -lt 2 ]]; then
        echo "error: --primary-uuid requires a value" >&2
        exit 1
      fi
      PRIMARY_UUID="$2"
      shift 2
      ;;
    --serial)
      if [[ $# -lt 2 ]]; then
        echo "error: --serial requires a value" >&2
        exit 1
      fi
      SERIAL="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done
require_primary_uuid

cage_it identity yubikey create integration-test-yk-touch \
  --slot 19 \
  --pin-policy never \
  --touch-policy always \
  $(serial_args) \
  $(force_slot_args) \
  $(yes_args)
create_provider_securely integration-test-yk-touch integration-test-yk-touch
cage_it environment create integration-test-yk-touch --provider integration-test-yk-touch --uuid "$PRIMARY_UUID" $(yes_args)
cage_it profile create integration-test-yubikey-touch --environments integration-test-yk-touch $(yes_args)

echo "yubikey touch-only integration profile configured at $CAGE_CONFIG"
