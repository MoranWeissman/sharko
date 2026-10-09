package crypto

import (
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	key := "my-secret-encryption-key-2026"
	plaintext := []byte(`{"old_git":{"provider":"azuredevops","pat":"super-secret-pat"},"old_argocd":{"token":"eyJhbGci..."}}`)

	encrypted, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	// Encrypted should be different from plaintext
	if encrypted == string(plaintext) {
		t.Fatal("encrypted text should differ from plaintext")
	}

	// Decrypt
	decrypted, err := Decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Errorf("expected %s, got %s", plaintext, decrypted)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key := "correct-key"
	plaintext := []byte("secret data")

	encrypted, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	_, err = Decrypt(encrypted, "wrong-key")
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestEncryptEmptyKey(t *testing.T) {
	_, err := Encrypt([]byte("data"), "")
	if err == nil {
		t.Fatal("expected error with empty key")
	}
}

func TestDecryptEmptyKey(t *testing.T) {
	_, err := Decrypt("somedata", "")
	if err == nil {
		t.Fatal("expected error with empty key")
	}
}

func TestEncryptProducesDifferentCiphertext(t *testing.T) {
	key := "my-key"
	plaintext := []byte("same data")

	enc1, _ := Encrypt(plaintext, key)
	enc2, _ := Encrypt(plaintext, key)

	// Different nonces should produce different ciphertext
	if enc1 == enc2 {
		t.Fatal("two encryptions of same data should differ (random nonce)")
	}

	// Both should decrypt to same plaintext
	dec1, _ := Decrypt(enc1, key)
	dec2, _ := Decrypt(enc2, key)
	if string(dec1) != string(dec2) {
		t.Fatal("both should decrypt to same value")
	}
}
