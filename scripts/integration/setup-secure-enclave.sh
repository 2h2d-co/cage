#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/common.sh"

usage() {
  cat <<'EOF'
Usage: scripts/integration/setup-secure-enclave.sh [--primary-uuid UUID] [--override]

Creates optional Secure Enclave integration config:
  identity:    integration-test-se
  provider:    integration-test-se
  environment: integration-test-se
  profile:     integration-test-secure-enclave

The Secure Enclave identity uses cage defaults: --access-control any-biometry,
--recipient-type se, and no --pq.
EOF
}

OVERRIDE=false
PRIMARY_UUID=""
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

cage_it identity se create integration-test-se $(yes_args)
create_provider_securely integration-test-se integration-test-se
cage_it environment create integration-test-se --provider integration-test-se --uuid "$PRIMARY_UUID" $(yes_args)
cage_it profile create integration-test-secure-enclave --environments integration-test-se $(yes_args)

echo "secure-enclave integration profile configured at $CAGE_CONFIG"
