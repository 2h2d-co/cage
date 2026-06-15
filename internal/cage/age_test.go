package cage

import (
	"path/filepath"
	"testing"

	"filippo.io/age"
)

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
