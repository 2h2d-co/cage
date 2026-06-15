# cage integration tests

The integration suite exercises cage end to end without checking any secret material into the repository. It uses a local `integration-test` cage profile with a basic age identity and an encrypted 1Password service account provider.

## Requirements

- macOS
- Go 1.26+
- A 1Password service account token with access to the test Environments below

The tests do **not** read the service account token from `OP_SERVICE_ACCOUNT_TOKEN`. Create the encrypted provider once during setup, then run the suite with the encrypted provider file on disk.

## Setup

The default integration config path is:

```sh
${XDG_CONFIG_HOME:-$HOME/.config}/cage/integration-test/config.toml
```

The `cage:it` mise shell alias sets `CAGE_CONFIG` to that path and runs `go run .`.

Use a different path by exporting `CAGE_CONFIG=/path/to/config.toml` and running `cage:local` instead. `CAGE_INTEGRATION_CONFIG=/path/to/config.toml` is also supported when running the tests if you need a test-only override.

1. Create two 1Password Environments the service account can read.

   Primary Environment variables:

   ```text
   CAGE_INTEGRATION_HEALTH=ok
   CAGE_INTEGRATION_ORDER=profile
   CAGE_INTEGRATION_EXEC=exec-ok
   CAGE_INTEGRATION_EDGE=value with spaces; equals=ok; pipe|ampersand&dollar$quote"apostrophe'backslash\done
   ```

   `CAGE_INTEGRATION_EDGE` intentionally includes whitespace, `=`, and shell metacharacters so `cage get` can be tested in pipes and shell command chains. Keep it single-line because `cage get '*'` is line-oriented.

   Override Environment variables:

   ```text
   CAGE_INTEGRATION_ORDER=explicit
   ```

2. Run the basic setup task. It wraps `scripts/integration/setup-basic.sh`, creates the basic identity, encrypted provider, two environment entries, and `integration-test` profile. Paste the service account token when prompted; the task does not read it from an environment variable.

   ```sh
   mise run integration:setup:basic --primary-uuid <primary-environment-uuid> --override-uuid <override-environment-uuid>
   ```

   Add `--override` to overwrite existing entries/files with the same names:

   ```sh
   mise run integration:setup:basic --primary-uuid <primary-environment-uuid> --override-uuid <override-environment-uuid> --override
   ```

3. Validate the profile manually:

   ```sh
   cage:it get --profiles integration-test CAGE_INTEGRATION_HEALTH
   cage:it get --profiles integration-test --environments integration-test-override CAGE_INTEGRATION_ORDER
   ```

   Expected output is `ok` for the first command and `explicit` for the second.

## Optional hardware identity profiles

The integration suite also runs these hardware identity profiles when they exist in the config:

- `integration-test-secure-enclave`
- `integration-test-yubikey-touch`
- `integration-test-yubikey-touch-pin`

If a profile is absent, its hardware subtest is skipped. Add profiles to the same integration config with the setup tasks below to enable those tests; no environment-variable toggle is used. Each setup task wraps a script in `scripts/integration/`, creates a separate encrypted provider, and prompts securely for the service account token.

Add `--override` to any setup task to overwrite existing entries/files with the same names. For YubiKey setup tasks, `--override` also overwrites the selected PIV slot.

### Secure Enclave

The default Secure Enclave access control is `any-biometry`.

```sh
mise run integration:setup:secure-enclave
```

If the primary `integration-test` environment does not exist yet, pass its UUID explicitly:

```sh
mise run integration:setup:secure-enclave --primary-uuid <primary-environment-uuid>
```

### YubiKey 4 Nano retired slots

`age-plugin-yubikey` uses YubiKey PIV retired key slots only. Its `--slot` flag is 1-indexed across retired slots, so `--slot 19` is PIV retired slot 19 / hex slot `94`, and `--slot 20` is PIV retired slot 20 / hex slot `95`. These are the last two retired slots on a YubiKey 4/5. YubiKey 4 firmware supports these retired slots and configurable PIN/touch policies; the tests use `always` touch rather than cached touch.

Use dedicated slots only. Passing `--force-slot` overwrites key material in that PIV slot, and cage cannot erase YubiKey key material later. Run YubiKey identity creation from an interactive terminal; the plugin prompts for the PIV PIN even when creating a key with `--pin-policy never`, because it needs management access to generate the key.

Touch-only profile on plugin slot 19 / PIV hex slot `94`:

```sh
mise run integration:setup:yubikey-touch
```

If more than one YubiKey is connected:

```sh
mise run integration:setup:yubikey-touch --serial <serial>
```

Touch-and-PIN profile on plugin slot 20 / PIV hex slot `95`:

```sh
mise run integration:setup:yubikey-touch-pin
```

If more than one YubiKey is connected:

```sh
mise run integration:setup:yubikey-touch-pin --serial <serial>
```

## Run

```sh
make integration-test
# or
mise run integration-test
# or
go test -v -count=1 -tags=integration ./integration
```

Run all configured profile tests without the local lifecycle/completion subtests:

```sh
mise run integration:run:all
```

Run one configured profile at a time:

```sh
mise run integration:run:basic
mise run integration:run:secure-enclave
mise run integration:run:yubikey-touch
mise run integration:run:yubikey-touch-pin
```

Hardware-backed profiles print an action-needed message on stderr and ask macOS Notification Center (via AppleScript) to show an action-needed notification before Secure Enclave approval, YubiKey PIN entry, or YubiKey touch is expected. YubiKey PIN prompts mention that touch follows PIN entry when the key blinks. Native notifications depend on macOS notification permissions for scripts.

If your config is not at the default path and you have not exported `CAGE_CONFIG`:

```sh
CAGE_CONFIG=/path/to/config.toml go test -v -count=1 -tags=integration ./integration
```

The suite runs with `go test -v` and prints a final `cage integration identity/profile results` summary. Optional hardware profiles show `status=missing` when their profile is not present in the integration config, `status=ok` when they pass, and `status=fail` when their subtest fails.

The suite removes any real `OP_SERVICE_ACCOUNT_TOKEN` from the cage subprocess environment. The `exec` test sets a fake parent value only to verify that cage strips it from the child process.
