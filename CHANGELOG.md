# Changelog

All notable changes to this project will be documented in this file.

This project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) style sections and uses semantic versioning once tagged releases begin.

## [Unreleased]

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
