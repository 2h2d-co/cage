# cage CLI Project Instructions

`cage` is a Go-based, macOS-only CLI secret manager: a minimal/opinionated alternative take on `fnox`.

## Conventions

- Format commit messages according to [Conventional Commits](https://www.conventionalcommits.org/).
- Maintain `CHANGELOG.md` using the [Keep a Changelog](https://keepachangelog.com/) style.
- Add changelog entries for changes whose commit would be `feat:` or `fix:`; keep entries under `Unreleased` until a release is made.
- Release commits should do the following:
  - update the project version;
  - move `Unreleased` changelog entries into the new release section;
  - commit with `release: vX.Y.Z` as the commit message;
  - tag the release with the matching `vX.Y.Z` tag.

## Core Constraints

- Global config only: `$XDG_CONFIG_HOME/cage/config.toml`, falling back to `~/.config/cage/config.toml`.
- `--config PATH` is supported for testing/development.
- No hierarchical configs or TUI.
- Relative identity/provider files resolve from the config file directory.
- Created identity files are named `NAME.identity` and written with mode `0600`.
- Encrypted 1Password provider files are named `NAME.1p.age` and written with mode `0600`.
- 1Password access uses the 1Password Go SDK, not the `op` CLI.
- Provider tokens must not be exposed to child processes; `OP_SERVICE_ACCOUNT_TOKEN` is stripped from `cage exec` children.
- 1Password Environments are only cached when an environment has `cache = { ttl = "...", identity = "..." }`; on-disk caches must always be age-encrypted.

## Config schema reminders

Supported types only:

- identities: `basic`, `yubikey`, `secure-enclave`
- providers: `1password`
- environments: `1password-environment`

Profiles are flat and reference only environments, not other profiles. There is no default profile.

Environment cache config is a nested inline object on environment entries: `cache = { ttl = "15m", identity = "local" }`. `ttl` is a positive Go duration string, `identity` may reference any configured identity, and active cache expiry is capped by the current config TTL.

Resolution rules for `get`/`exec`:

- `--profiles` and `--environments` are comma-separated.
- `--skip-cache` means no cache reads or writes; `--refresh-cache` means pull fresh and write configured caches.
- Flags override `CAGE_PROFILES` and `CAGE_ENVIRONMENTS`.
- Load profiles first in argument order.
- Load explicit environments after profiles in argument order.
- Last loaded environment variable wins.

## Important implementation details

- CLI is built with Cobra in `internal/cage/root.go`.
- `main.go` wires the root command and redacted error output. Startup encrypted cache cleanup is triggered from the root Cobra persistent pre-run and skipped for read-only diagnostics/status commands.
- age support is in `internal/cage/age.go`.
  - Native identities use `filippo.io/age`.
  - Plugin identities use `filippo.io/age/plugin`.
- 1Password Environment resolution is in `internal/cage/resolve.go`.
- Encrypted Environment cache storage and cleanup are in `internal/cage/cache.go`; cache management commands are in `internal/cage/cache_command.go`; launchd prune scheduling commands are in `internal/cage/cache_launchd_command.go`; read-only diagnostics are in `internal/cage/doctor_command.go`.
- Cache files live under `${XDG_CACHE_HOME:-$HOME/.cache}/cage/environments/`; cache state is `${XDG_STATE_HOME:-$HOME/.local/state}/cage/cage.db`.
- Periodic cache pruning is managed by a per-user launchd LaunchAgent at `~/Library/LaunchAgents/co.2h2d.cage.cache-prune.plist`; logs are `~/Library/Logs/co.2h2d.cage.cache-prune.log` and `~/Library/Logs/co.2h2d.cage.cache-prune-error.log`.
- Expired, inactive, unreadable, and replaced cache files should be removed with normal file deletion; do not add overwrite passes for APFS/SSD storage.
- `cage exec` uses process replacement semantics via `golang.org/x/sys/unix.Exec`.
- `gosec` G304 is handled by cleaning file paths before reads; avoid adding `#nosec`.
- Plugin command execution intentionally avoids `exec.Command` to keep `gosec` clean.
- Release builds require CGO because the 1Password Go SDK beta needs CGO for darwin cross-compilation; do not force `CGO_ENABLED=0` in release builds.

## Identity/provider/environment/profile command behavior

- `cage identity basic create NAME` generates a native age identity and updates `[identities]`.
- `cage identity yubikey create NAME` calls `age-plugin-yubikey` and updates `[identities]`.
- `cage identity se create NAME` calls `age-plugin-se` and updates `[identities]`.
- `cage provider 1p create NAME --identity IDENTITY` reads a token securely or from stdin, encrypts to only that identity, and updates `[providers]`.
- `cage environment create NAME --provider PROVIDER --uuid UUID` creates a `1password-environment` entry and updates `[environments]`; add `--cache-ttl DURATION --cache-identity IDENTITY` to enable encrypted caching.
- `cage environment cache set NAME --ttl DURATION --identity IDENTITY` adds cache settings; pass `--overwrite` with both settings to replace existing cache settings.
- `cage environment cache unset NAME` removes cache settings; use `cage cache clear NAME` to remove existing encrypted cache data.
- `cage cache list/status/prune/clear` inspects and manages encrypted cache metadata/files without printing secret values.
- `cage cache launchd install/uninstall` installs or removes the per-user periodic prune LaunchAgent. Install uses the current executable absolute path and active config absolute path. `CAGE_CACHE_PRUNE_LAUNCHD_LABEL` overrides the default launchd label for parallel/testing setups.
- `cage doctor` is read-only and metadata-only: it must not decrypt provider tokens, decrypt cached Environment payloads, contact 1Password, prompt hardware-backed identities, or prune cache files.
- `cage profile create NAME --environments ENV[,ENV...]` creates a flat profile and updates `[profiles]`.
- Environment deletion is blocked while a profile references that environment.
- `delete` removes cage config entries and local files after confirmation.
- `age-plugin-yubikey` does not expose key-material deletion; do not claim YubiKey material is erased.

## Linting/security expectations

Do not lower thresholds, disable linters, or add suppressions just to get green builds.

Expectations:

- No `nolint`.
- No `#nosec`.
- No golangci-lint `disable` or `exclude-rules`.
- Zizmor runs as `zizmor --pedantic --no-ignores .github/workflows`.
- GitHub Actions are pinned by full commit SHA and use `persist-credentials: false`.

Recommended validation:

```sh
go mod verify
test -z "$(gofmt -l .)"
go test -race -mod=readonly ./...
go vet ./...
mise run lint
goreleaser check
```

If mise config is untrusted in a non-interactive harness, run commands with:

```sh
export MISE_TRUSTED_CONFIG_PATHS=$PWD
```

## Tooling files

- `mise.toml` defines Go, age, age plugins, actionlint, zizmor, golangci-lint, and GoReleaser.
- `mise.lock` is present and should be kept in sync after tool changes.
- `.golangci.yml` intentionally enables established Go quality/security linters.
- `.goreleaser.yaml` builds darwin/arm64 releases.
- `Makefile` mirrors common tasks.

## Documentation/metadata

- License: MIT, copyright `Two Humans and Two Dogs LLC (2h2d.co)`.
- Repo URL: `https://github.com/2h2d-co/cage`.
- Keep README, `examples/config.toml`, command help, shell completions, and manpage support aligned with CLI behavior.
