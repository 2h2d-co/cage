# cage

`cage` is a minimal, opinionated, macOS-only CLI for loading 1Password Environments with age-protected 1Password service account tokens.

It is a different take on tools like `fnox`: configuration is global, profile/environment resolution is flat, and secrets are fetched only for the command being run.

Repository: <https://github.com/2h2d-co/cage>

## Status

Initial implementation:

- Global config only: `$XDG_CONFIG_HOME/cage/config.toml`, falling back to `~/.config/cage/config.toml`
- Development/testing override: `CAGE_CONFIG=PATH` or `--config PATH`
- macOS only
- native age identity creation/listing/deletion from cage config
- age Go library with plugin protocol support
- age-plugin-yubikey identity creation/listing/deletion from cage config
- age-plugin-se identity creation/listing/deletion from cage config
- encrypted 1Password service account providers as `NAME.1p.age`
- 1Password Environment and profile config management
- `cage get` and `cage exec`
- shell completions and manpage generation

No TUI, hierarchical config, or environment caching is implemented.

## Requirements

- macOS
- Go 1.26+
- `age-plugin-yubikey` for YubiKey identities
- `age-plugin-se` for Secure Enclave identities

Install hints used by cage:

```sh
brew install age
brew install age-plugin-yubikey age-plugin-se # if you use those identity types
```

## Install

For macOS Apple Silicon with mise:

```sh
mise use -g github:2h2d-co/cage@0.0.3
# or in a project mise.toml:
# "github:2h2d-co/cage" = "0.0.3"
```

The GitHub release publishes a `darwin_arm64` archive with the `cage` binary at the archive root, plus checksums and GitHub artifact attestations for mise's `github:` backend.

For Go users:

```sh
go install github.com/2h2d-co/cage@v0.0.3
```

For local development:

```sh
mise install
make test
make build
```

With mise shell integration active, `cage:local` is an alias for `go run .`, and `cage:it` runs local cage with `CAGE_CONFIG=${XDG_CONFIG_HOME:-$HOME/.config}/cage/integration-test/config.toml`.

## Config

The config file is `$XDG_CONFIG_HOME/cage/config.toml`, falling back to `~/.config/cage/config.toml` when `XDG_CONFIG_HOME` is unset. Set `CAGE_CONFIG=PATH` to use another config, or pass `--config PATH` to override both. Identity and provider file paths must be relative paths that stay within the config file directory.

Example:

```toml
[identities]
local = { type = "basic", file = "local.identity" }
work1 = { type = "yubikey", file = "work1.identity" }
work2 = { type = "secure-enclave", file = "work2.identity" }

[providers]
project1 = { type = "1password", identity = "local", file = "project1.1p.age" }
project2 = { type = "1password", identity = "work2", file = "project2.1p.age" }

[environments]
dev = { type = "1password-environment", provider = "project1", uuid = "00000000-0000-0000-0000-000000000000", cache = { ttl = "15m", identity = "local" } }
stage = { type = "1password-environment", provider = "project2", uuid = "11111111-1111-1111-1111-111111111111" }

[profiles]
default = ["dev"]
proj2-prod = ["dev", "stage"]
```

Supported types:

- identities: `basic`, `yubikey`, `secure-enclave`
- providers: `1password`
- environments: `1password-environment`

Profiles can only reference environments. They cannot reference other profiles.

Environment caches are optional. Add `cache = { ttl = "15m", identity = "local" }` to an environment to cache that Environment for a positive Go duration string encrypted to the named identity. Changing `ttl` caps already-written entries by the current config TTL. Cached Environment files are stored under `${XDG_CACHE_HOME:-$HOME/.cache}/cage/environments/`; cache state is tracked in `${XDG_STATE_HOME:-$HOME/.local/state}/cage/cage.db`.

## Identity management

Created identity names must use only letters, numbers, `_`, and `-`. Created files are always named `NAME.identity` and written with mode `0600`.

```sh
cage identity basic create local
cage identity basic create local-pq --pq
cage identity basic list
cage identity basic delete local

cage identity yubikey create work1
cage identity yubikey list
cage identity yubikey list --connected
cage identity yubikey delete work1

cage identity se create work2
cage identity se list
cage identity se delete work2
```

Basic identities are native age X25519 identities by default; pass `--pq` to generate a native post-quantum age identity.
YubiKey options include `--serial`, `--slot`, `--pin-policy`, `--touch-policy`, and `--force-slot`.
Secure Enclave options include `--access-control`, `--recipient-type`, and `--pq`; `--access-control` defaults to `any-biometry`.

`delete` removes the cage config entry and local identity file. `age-plugin-yubikey` does not expose key-material deletion, so YubiKey key material is not erased by cage.

## 1Password providers

Create an encrypted 1Password service account provider:

```sh
cage provider 1p create project1 --identity local
```

If stdin is not a terminal, cage reads the token from stdin. You can also force stdin:

```sh
printf '%s' "$OP_SERVICE_ACCOUNT_TOKEN" | cage provider 1p create project1 --identity local --stdin
```

The plaintext token is encrypted only to the configured identity and stored as `project1.1p.age` with mode `0600`. cage does not expose provider tokens to child processes.

## Environment and profile management

Create, list, and delete 1Password Environment config entries:

```sh
cage environment create dev --provider project1 --uuid 00000000-0000-0000-0000-000000000000
cage environment create dev-cached --provider project1 --uuid 00000000-0000-0000-0000-000000000000 --cache-ttl 15m --cache-identity local
cage environment list
cage environment delete dev
```

Create, list, and delete flat profiles:

```sh
cage profile create default --environments dev,stage
cage profile list
cage profile delete default
```

Environment deletion is blocked while a profile still references that environment. Recreate or delete the profile first.

## Resolution rules

`--profiles` and `--environments` accept comma-separated names.

- Flags override `CAGE_PROFILES` and `CAGE_ENVIRONMENTS`.
- Profiles load first, in the given order.
- Environments from `--environments` load after profiles, in the given order.
- Last loaded environment variable wins.
- There is no default profile; selecting no profile/environment is an error.

If an environment has encrypted caching configured, `get` and `exec` use an active cache before pulling from 1Password. `--skip-cache` avoids both cache reads and writes. `--refresh-cache` pulls fresh values and updates configured caches. If a cache entry is unreadable, cage deletes it and pulls fresh values.

## Get

Print one value:

```sh
cage get --profiles default DATABASE_URL
```

Print all values as dotenv-style lines:

```sh
cage get --profiles default '*'
```

Use this form for quick inspection or simple single-line values. Values are not shell-quoted; multiline values span multiple output lines and are not robust for parsing. Prefer JSON for multiline secrets or machine-readable output.

Print JSON:

```sh
cage get --profiles default --json '*'
```

Bypass configured caches:

```sh
cage get --profiles default --skip-cache '*'
cage get --profiles default --refresh-cache '*'
```

Missing variables print an error and exit with status `1`.

## Exec

Run a command with parent environment plus resolved cage variables:

```sh
cage exec --profiles default -- npm run start
cage exec --profiles default --refresh-cache -- npm run start
```

On macOS, cage uses `exec` to replace itself with the child process. `OP_SERVICE_ACCOUNT_TOKEN` is removed from the child environment even if present in the parent environment or loaded environment.

## Integration tests

An optional end-to-end integration suite lives in [`integration/`](integration/). It requires a local `integration-test` cage profile, identity, encrypted provider file, and 1Password Environments; no secret material is checked into this repository. The local setup can use the mise shell alias `cage:it`.

```sh
mise run integration:run:all
```

See [`integration/README.md`](integration/README.md) for setup instructions.

## Shell completions and manpages

```sh
cage completion zsh > _cage
cage completion bash > cage.bash
cage completion fish > cage.fish

cage man ./man
man ./man/cage.1
```

A checked-in starter manpage is also available at `docs/man/cage.1`.

## Security/UX notes

- Provider tokens are decrypted only in memory to initialize the 1Password SDK.
- 1Password Environments are not cached in memory across environment loads. Optional on-disk Environment caches are always age-encrypted to the configured cache identity.
- Expired, inactive, unreadable, and replaced cache files are removed with normal file deletion; cache secrecy relies on age encryption, not overwrite passes.
- Config directories, config files, identity files, provider files, cache directories, cached Environment files, and the cache state database must be owned by the current user and not accessible by group or others.
- Cage strips common credential, injection, and debug environment variables from age plugin subprocesses.
- Errors are redacted for common secret-looking values before cage prints them.
- Use `--verbose` for high-level diagnostics or `--debug` for extra details; secret values are not intentionally logged.

## License

MIT © Two Humans and Two Dogs LLC (2h2d.co)
