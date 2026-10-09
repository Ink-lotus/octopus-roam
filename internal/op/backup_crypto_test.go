package op

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestEncryptedBackupRoundTripAndTampering(t *testing.T) {
	password := "  example backup password  "
	dump := &model.DBDump{Product: "octopus-roam", Version: 1000,
		ChannelKeys: []model.ChannelKey{{ChannelKeyConfig: model.ChannelKeyConfig{Key: "synthetic-secret-in-backup", Enabled: false}}},
	}
	raw, err := EncryptBackup(dump, password)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("synthetic-secret-in-backup")) || bytes.Contains(raw, []byte(password)) {
		t.Fatal("plaintext leaked")
	}
	second, err := EncryptBackup(dump, password)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(raw, second) {
		t.Fatal("encryption reused randomness")
	}
	restored, err := DecryptBackup(raw, password)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Product != "octopus-roam" || restored.Version != 1000 || restored.ChannelKeys[0].Key != dump.ChannelKeys[0].Key || restored.ChannelKeys[0].Enabled {
		t.Fatal("round trip lost data")
	}
	if _, err := DecryptBackup(raw, strings.TrimSpace(password)); !errors.Is(err, ErrBackupDecrypt) {
		t.Fatalf("password was normalized: %v", err)
	}
	var envelope encryptedBackup
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"salt", "nonce", "ciphertext", "product", "version", "kdf", "cipher", "format"} {
		t.Run(field, func(t *testing.T) {
			modified := envelope
			modified.Salt = bytes.Clone(envelope.Salt)
			modified.Nonce = bytes.Clone(envelope.Nonce)
			modified.Ciphertext = bytes.Clone(envelope.Ciphertext)
			switch field {
			case "salt":
				modified.Salt[0] ^= 1
			case "nonce":
				modified.Nonce[0] ^= 1
			case "ciphertext":
				modified.Ciphertext[0] ^= 1
			case "product":
				modified.Product = "other"
			case "version":
				modified.Version++
			case "kdf":
				modified.KDF = "scrypt-n999999999-r8-p1"
			case "cipher":
				modified.Cipher = "unknown"
			case "format":
				modified.Format = "unknown"
			}
			input, _ := json.Marshal(modified)
			if _, err := DecryptBackup(input, password); err == nil {
				t.Fatal("tampered backup accepted")
			}
		})
	}
	for _, invalid := range [][]byte{raw[:len(raw)/2], append(bytes.Clone(raw), raw...), []byte("null"), []byte(`{"version":1000}`), bytes.Replace(raw, []byte(`"format":`), []byte(`"extra":true,"format":`), 1)} {
		if _, err := DecryptBackup(invalid, password); err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
}

func TestBackupCryptoLimits(t *testing.T) {
	dump := &model.DBDump{Product: "octopus-roam", Version: 1000}
	for _, password := range []string{"", "short", strings.Repeat("a", 1025)} {
		if _, err := EncryptBackup(dump, password); !errors.Is(err, ErrBackupPassword) {
			t.Fatalf("invalid password: %v", err)
		}
	}
	if _, err := EncryptBackup(&model.DBDump{Version: 6}, "long-test-password"); err == nil {
		t.Fatal("encrypted ambiguous backup")
	}
	writer := limitedBackupBuffer{limit: 5}
	if _, err := writer.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("6")); !errors.Is(err, ErrBackupTooLarge) {
		t.Fatal("size limit ignored")
	}
	if writer.Len() != 5 {
		t.Fatal("oversized input retained")
	}
}
