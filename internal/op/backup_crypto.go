package op

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"golang.org/x/crypto/scrypt"
)

const (
	MaxBackupPlaintextBytes = 64 << 20
	MaxBackupRequestBytes   = 96 << 20
	backupKDF               = "scrypt-n32768-r8-p1"
	backupCipher            = "aes-256-gcm"
	backupAAD               = "octopus-roam\nencrypted-backup\n1\nscrypt-n32768-r8-p1\naes-256-gcm"
)

var (
	ErrBackupTooLarge = errors.New("backup exceeds the size limit")
	ErrBackupPassword = errors.New("backup password must contain at least 12 characters and at most 1024 bytes")
	ErrBackupInvalid  = errors.New("invalid or unsupported encrypted backup")
	ErrBackupDecrypt  = errors.New("incorrect backup password or damaged backup")
)

type encryptedBackup struct {
	Product    string `json:"product"`
	Format     string `json:"format"`
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Cipher     string `json:"cipher"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

type limitedBackupBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBackupBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, ErrBackupTooLarge
	}
	return b.Buffer.Write(p)
}

func ValidateBackupExportPassword(password string) error {
	if !utf8.ValidString(password) || len(password) > 1024 || utf8.RuneCountInString(password) < 12 {
		return ErrBackupPassword
	}
	return nil
}

func backupAEAD(password string, salt []byte) (cipher.AEAD, error) {
	// 参数由格式版本固定，不接受文件指定的任意内存/CPU 成本。
	key, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// EncryptBackup 加密完整内容。调用者负责认证、请求取消与并发限制。
func EncryptBackup(dump *model.DBDump, password string) ([]byte, error) {
	if err := ValidateBackupExportPassword(password); err != nil {
		return nil, err
	}
	if err := validateDumpOrigin(dump); err != nil {
		return nil, err
	}
	buffer := limitedBackupBuffer{limit: MaxBackupPlaintextBytes}
	defer func() { clear(buffer.Bytes()) }()
	if err := json.NewEncoder(&buffer).Encode(dump); err != nil {
		return nil, err
	}
	envelope := encryptedBackup{
		Product: dumpProduct, Format: "encrypted-backup", Version: 1,
		KDF: backupKDF, Cipher: backupCipher,
		Salt: make([]byte, 16), Nonce: make([]byte, 12),
	}
	if _, err := rand.Read(envelope.Salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(envelope.Nonce); err != nil {
		return nil, err
	}
	aead, err := backupAEAD(password, envelope.Salt)
	if err != nil {
		return nil, err
	}
	envelope.Ciphertext = aead.Seal(nil, envelope.Nonce, buffer.Bytes(), []byte(backupAAD))
	return json.Marshal(envelope)
}

// DecryptBackup 在认证通过后才解析内层数据；任何失败都不回退明文。
func DecryptBackup(body []byte, password string) (*model.DBDump, error) {
	if len(body) > MaxBackupRequestBytes {
		return nil, ErrBackupTooLarge
	}
	if password == "" || len(password) > 1024 || !utf8.ValidString(password) {
		return nil, ErrBackupDecrypt
	}
	var envelope encryptedBackup
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, ErrBackupInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ErrBackupInvalid
	}
	if envelope.Product != dumpProduct || envelope.Format != "encrypted-backup" || envelope.Version != 1 ||
		envelope.KDF != backupKDF || envelope.Cipher != backupCipher || len(envelope.Salt) != 16 ||
		len(envelope.Nonce) != 12 || len(envelope.Ciphertext) < 16 {
		return nil, ErrBackupInvalid
	}
	if len(envelope.Ciphertext) > MaxBackupPlaintextBytes+16 {
		return nil, ErrBackupTooLarge
	}
	aead, err := backupAEAD(password, envelope.Salt)
	if err != nil {
		return nil, ErrBackupInvalid
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, []byte(backupAAD))
	if err != nil {
		return nil, ErrBackupDecrypt
	}
	defer clear(plaintext)
	var dump model.DBDump
	if err := json.Unmarshal(plaintext, &dump); err != nil {
		return nil, ErrBackupInvalid
	}
	if err := validateDumpOrigin(&dump); err != nil {
		return nil, err
	}
	return &dump, nil
}
