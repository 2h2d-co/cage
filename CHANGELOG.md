# Changelog

All notable changes to this project will be documented in this file.

This project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) style sections and semantic versioning.

## [Unreleased]

## [0.0.9] - 2026-06-22

### Added

- Added installation instructions for Homebrew, mise, manual tarball downloads, and `go install`.
- Added Homebrew tap support for installing Apple Silicon macOS release builds.

## [0.0.8] - 2026-06-19

### Added

- Added `cage identity list`, `cage provider list`, and `cage environment inspect NAME` so you can review configured items without exposing secret values.

### Changed

- Management commands now use consistent non-secret `key=value` output and per-row status information.

## [0.0.7] - 2026-06-18

### Added

- Added `cage doctor`, a read-only diagnostic command that checks config, local files, identities, providers, and encrypted cache state without decrypting secrets or contacting 1Password.

## [0.0.6] - 2026-06-17

### Added

- Added `cage cache launchd install` and `cage cache launchd uninstall` so macOS can prune expired encrypted cache files periodically in the background.

## [0.0.5] - 2026-06-17

### Added

- Added `cage cache list`, `cage cache status`, `cage cache prune`, and `cage cache clear` for viewing and managing encrypted Environment caches without printing secret values.
- Added JSON output for cache list and status commands.
- Added `cage environment cache set --overwrite` and `cage environment cache unset` to enable, change, or remove cache settings for an environment.

## [0.0.4] - 2026-06-16

### Added

- Added optional age-encrypted caching for 1Password Environments with per-environment TTL and cache identity settings.
- Added `--skip-cache` and `--refresh-cache` to `cage get` and `cage exec`.

### Changed

- Encrypted Environment caches are stored under the user's XDG cache directory, with cache state tracked under the user's XDG state directory.

### Security

- Expired, inactive, unreadable, and replaced encrypted Environment cache files are deleted automatically during cache maintenance.

## [0.0.3] - 2026-06-16

### Changed

- Hardware-backed decrypt flows now provide clearer terminal prompts when user action is needed.
- Config parsing and environment handling now report invalid input more consistently.

### Security

- Decrypted 1Password provider tokens are wiped from Cage-owned memory immediately after creating the 1Password client, reducing exposure in crash dumps or process-memory inspection.
- `cage exec` and age plugin operations strip common credential, injection, and debug environment variables before launching child processes.
- Identity and provider file paths must stay inside the config directory and cannot escape through absolute paths, `..`, or symlinks.
- Config, identity, and provider files must be owned by the current user and must not be readable or writable by group/others.
- Invalid nested TOML keys, non-string scalar fields, and unsafe environment names are rejected.
- Provider tokens can be encrypted using a public recipient without parsing private identity material.
- Secret file writes are synced before rename and avoid unnecessary permission changes after rename.

## [0.0.2] - 2026-06-15

### Added

- GitHub Releases now provide Apple Silicon macOS (`darwin_arm64`) downloads and checksums.

### Changed

- This release is functionally identical to `0.0.1`.

## [0.0.1] - 2026-06-15

### Added

- Initial macOS-only `cage` CLI.
- Global TOML config loading with `CAGE_CONFIG` and `--config` overrides.
- Basic native age identity management with X25519 and post-quantum identity creation.
- YubiKey and Secure Enclave age-plugin identity management.
- Encrypted 1Password service account provider files.
- 1Password Environment and flat profile management commands.
- `cage get` for single-variable, all-variable, and JSON output.
- `cage exec` for running commands with resolved secrets while stripping `OP_SERVICE_ACCOUNT_TOKEN` from the child environment.
- Shell completion and manpage generation commands.
- Terminal prompts for hardware-backed decrypt flows that need user action.
- Apple Silicon macOS (`darwin_arm64`) release archives compatible with mise's `github:` backend.

### Security

- Provider tokens are encrypted to configured age identities and stored with mode `0600`.
- Created identity files are written with mode `0600`.
- 1Password Environments are not cached on disk.
