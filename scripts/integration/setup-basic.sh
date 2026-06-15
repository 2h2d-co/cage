#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/common.sh"

usage() {
  cat <<'EOF'
Usage: scripts/integration/setup-basic.sh --primary-uuid UUID --override-uuid UUID [--override]

Creates the required basic integration identity, provider, environments, and profile:
  identity:     integration-test
  provider:     integration-test
  environments: integration-test, integration-test-override
  profile:      integration-test
EOF
}

OVERRIDE=false
PRIMARY_UUID=""
OVERRIDE_UUID=""
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
    --override-uuid)
      if [[ $# -lt 2 ]]; then
        echo "error: --override-uuid requires a value" >&2
        exit 1
      fi
      OVERRIDE_UUID="$2"
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

if [[ -z "$PRIMARY_UUID" ]]; then
  PRIMARY_UUID="$(environment_uuid integration-test)"
fi
if [[ -z "$OVERRIDE_UUID" ]]; then
  OVERRIDE_UUID="$(environment_uuid integration-test-override)"
fi
if [[ -z "$PRIMARY_UUID" ]]; then
  echo "error: --primary-uuid is required" >&2
  exit 1
fi
if [[ -z "$OVERRIDE_UUID" ]]; then
  echo "error: --override-uuid is required" >&2
  exit 1
fi

cage_it identity basic create integration-test $(yes_args)
create_provider_securely integration-test integration-test
cage_it environment create integration-test --provider integration-test --uuid "$PRIMARY_UUID" $(yes_args)
cage_it environment create integration-test-override --provider integration-test --uuid "$OVERRIDE_UUID" $(yes_args)
cage_it profile create integration-test --environments integration-test $(yes_args)

echo "basic integration profile configured at $CAGE_CONFIG"
