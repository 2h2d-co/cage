#!/usr/bin/env bash
set -euo pipefail

go test -v -count=1 -tags=integration ./integration -run '^TestEndToEnd$/^configured_1Password_profiles$/^profile_integration-test($|-.*)$'
