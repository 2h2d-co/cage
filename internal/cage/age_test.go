package cage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestYubiKeyPluginMessagesGuidePinThenTouch(t *testing.T) {
	if got, want := pluginSecretInputMessage("yubikey"), "enter the YubiKey PIN, then touch the YubiKey when it blinks"; got != want {
		t.Fatalf("pluginSecretInputMessage() = %q, want %q", got, want)
	}
	if got, want := pluginWaitMessage("yubikey"), "touch the YubiKey when it blinks"; got != want {
		t.Fatalf("pluginWaitMessage() = %q, want %q", got, want)
	}
}

func TestPluginMessagesKeepGenericFallbacks(t *testing.T) {
	if got, want := pluginSecretInputMessage("se"), "age-plugin-se needs secure input"; got != want {
		t.Fatalf("pluginSecretInputMessage() = %q, want %q", got, want)
	}
	if got, want := pluginWaitMessage("se"), "age-plugin-se is waiting for hardware or user confirmation"; got != want {
		t.Fatalf("pluginWaitMessage() = %q, want %q", got, want)
	}
}

func TestReadAgeIdentitiesRejectsInsecurePermissions(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	identityFile := filepath.Join(t.TempDir(), "test.identity")
	if err := os.WriteFile(identityFile, []byte(identity.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	makeInsecurePermissions(t, identityFile, 0o644)

	_, err = readAgeIdentities(identityFile)
	if err == nil {
		t.Fatal("readAgeIdentities accepted group-readable identity file")
	}
	if !strings.Contains(err.Error(), "accessible by group or others") {
		t.Fatalf("error = %q, want permission error", err)
	}
}

func TestEncryptDecryptWithSingleNativeIdentity(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	identityFile := filepath.Join(t.TempDir(), "test.identity")
	if err := writeSecretFile(identityFile, []byte(identity.String()+"\n")); err != nil {
		t.Fatal(err)
	}

	ciphertext, err := encryptWithSingleIdentity([]byte("secret"), identityFile)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := decryptWithIdentityFile(ciphertext, identityFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "secret" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}
