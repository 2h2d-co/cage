# Changelog

All notable changes to this project will be documented in this file.

This project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) style sections and uses semantic versioning once tagged releases begin.

## [Unreleased]

## [0.0.5] - 2026-06-17

### Added

- Add `cage cache list`, `cage cache status`, `cage cache prune`, and `cage cache clear` for encrypted Environment cache metadata and lifecycle management.
- Add JSON output for cache list/status commands.
- Add `cage environment cache set --overwrite` and `cage environment cache unset` for editing per-environment cache settings.

## [0.0.4] - 2026-06-16

### Added

- Add optional age-encrypted 1Password Environment caching with per-environment TTL and cache identity settings.
- Store encrypted Environment cache files under XDG cache data and track cache state in an XDG state `cage.db` SQLite database.
- Add `--skip-cache` and `--refresh-cache` for `get` and `exec`.
- Add ephemeral integration coverage for encrypted cache bootstrap, cache hits, mixed cached/uncached loads, skip-cache, refresh-cache, exec, repair, failure, and cleanup paths.

### Security

- Remove expired, inactive, unreadable, and replaced encrypted Environment cache files with normal file deletion.

## [0.0.3] - 2026-06-16

### Changed

- Improve terminal action-needed prompts for hardware-backed decrypt flows.
- Clean up config parsing and environment handling.

### Security

- Reduce provider token plaintext lifetime and harden redacted error zeroing.
- Strip common credential, injection, and debug environment variables from child processes and age plugin subprocesses.
- Reject identity and provider file paths that are absolute, escape the config directory, or resolve through symlinks.
- Reject config directories, config files, identity files, and provider files not owned by the current user or accessible by group/others.
- Reject unsupported nested TOML config keys, non-string scalar fields, and unsafe environment names.
- Encrypt provider tokens without parsing private identity material when a public recipient is sufficient.
- Remove a redundant post-rename chmod from atomic writes and sync temp files before rename.

## [0.0.2] - 2026-06-15

### Changed

- Published the GitHub release with macOS Apple Silicon (`darwin_arm64`) artifacts and checksums.
- No source changes from `0.0.1`; both tags point at the same commit.

## [0.0.1] - 2026-06-15

### Added

- Initial macOS-only `cage` CLI.
- Global TOML config loading with `CAGE_CONFIG` and `--config` overrides.
- Basic native age identity management with X25519 and post-quantum identity creation.
- YubiKey and Secure Enclave age-plugin identity management.
- Encrypted 1Password service account provider files.
- 1Password Environment and flat profile management commands.
- `cage get` for single, all, and JSON environment variable output.
- `cage exec` with process replacement and `OP_SERVICE_ACCOUNT_TOKEN` stripping.
- Shell completion and manpage generation commands.
- End-to-end integration suite with basic, Secure Enclave, and YubiKey profiles.
- Mise setup/run tasks and standalone scripts for integration profiles.
- Terminal action-needed prompts for hardware-backed decrypt flows.
- GitHub release packaging for macOS Apple Silicon (`darwin_arm64`) compatible with mise's `github:` backend.

### Security

- Provider tokens are encrypted to configured age identities and stored with mode `0600`.
- Created identity files are written with mode `0600`.
- 1Password Environments are never cached on disk.
