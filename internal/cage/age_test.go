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
	identityData := []byte("# public key: " + identity.Recipient().String() + "\n" + identity.String() + "\n")
	if err := writeSecretFile(identityFile, identityData); err != nil {
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

func TestEncryptWithSingleIdentityUsesPublicRecipientComment(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	encryptionIdentityFile := filepath.Join(dir, "encrypt.identity")
	encryptionData := []byte("# public key: " + identity.Recipient().String() + "\nnot an age identity\n")
	if err := writeSecretFile(encryptionIdentityFile, encryptionData); err != nil {
		t.Fatal(err)
	}

	ciphertext, err := encryptWithSingleIdentity([]byte("secret"), encryptionIdentityFile)
	if err != nil {
		t.Fatal(err)
	}

	decryptionIdentityFile := filepath.Join(dir, "decrypt.identity")
	decryptionData := []byte("# public key: " + identity.Recipient().String() + "\n" + identity.String() + "\n")
	if err := writeSecretFile(decryptionIdentityFile, decryptionData); err != nil {
		t.Fatal(err)
	}
	plaintext, err := decryptWithIdentityFile(ciphertext, decryptionIdentityFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "secret" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}

func TestEncryptWithSingleIdentityRequiresPublicRecipientComment(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	identityFile := filepath.Join(t.TempDir(), "test.identity")
	if err := writeSecretFile(identityFile, []byte(identity.String()+"\n")); err != nil {
		t.Fatal(err)
	}

	_, err = encryptWithSingleIdentity([]byte("secret"), identityFile)
	if err == nil {
		t.Fatal("encryptWithSingleIdentity accepted an identity file without a public recipient comment")
	}
	if !strings.Contains(err.Error(), "no public recipient comment") {
		t.Fatalf("error = %q, want missing public recipient comment", err)
	}
}
