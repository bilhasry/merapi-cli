package envs

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/bilhasry/merapi/internal/project"
	"golang.org/x/crypto/scrypt"
)

// SecretsKeyEnvVar names the environment variable holding the passphrase used
// to encrypt/decrypt the per-environment secret stores.
const SecretsKeyEnvVar = "MERAPI_SECRETS_KEY"

var magic = []byte("APIMANS1")

const (
	saltLen  = 16
	nonceLen = 12
	scryptN  = 1 << 15
	scryptR  = 8
	scryptP  = 1
	keyLen   = 32
)

// ErrNoPassphrase is returned when the encryption key is not available.
var ErrNoPassphrase = fmt.Errorf(
	"secrets are encrypted; set the %s environment variable to a strong passphrase (it must stay consistent for the team)",
	SecretsKeyEnvVar,
)

// Passphrase reads the encryption passphrase from the environment.
func Passphrase() ([]byte, error) {
	v := os.Getenv(SecretsKeyEnvVar)
	if v == "" {
		return nil, ErrNoPassphrase
	}
	if len(v) < 8 {
		return nil, fmt.Errorf("%s must be at least 8 characters", SecretsKeyEnvVar)
	}
	return []byte(v), nil
}

// LoadSecrets reads and decrypts the secret store for an environment.
// A missing store yields an empty map.
func LoadSecrets(p *project.Project, envName string) (map[string]string, error) {
	path := p.SecretsPath(envName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	pass, err := Passphrase()
	if err != nil {
		return nil, err
	}
	secrets, err := decrypt(data, pass)
	if err != nil {
		return nil, fmt.Errorf("decrypting %s: %w (wrong %s?)", path, err, SecretsKeyEnvVar)
	}
	return secrets, nil
}

// SaveSecrets encrypts and writes the secret store for an environment.
func SaveSecrets(p *project.Project, envName string, secrets map[string]string) error {
	pass, err := Passphrase()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.LocalPath(), 0o755); err != nil {
		return err
	}
	blob, err := encrypt(secrets, pass)
	if err != nil {
		return err
	}
	return os.WriteFile(p.SecretsPath(envName), blob, 0o600)
}

// SetSecret upserts one secret.
func SetSecret(p *project.Project, envName, key, value string) error {
	if err := ValidateVariableKey(key); err != nil {
		return err
	}
	secrets, err := LoadSecrets(p, envName)
	if err != nil {
		return err
	}
	secrets[key] = value
	return SaveSecrets(p, envName, secrets)
}

// UnsetSecret deletes one secret.
func UnsetSecret(p *project.Project, envName, key string) error {
	secrets, err := LoadSecrets(p, envName)
	if err != nil {
		return err
	}
	if _, ok := secrets[key]; !ok {
		return fmt.Errorf("secret %q not set in environment %q", key, envName)
	}
	delete(secrets, key)
	return SaveSecrets(p, envName, secrets)
}

// encrypt seals a JSON-compatible string map: MAGIC | salt | nonce | ct.
func encrypt(data map[string]string, passphrase []byte) ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key(passphrase, salt, scryptN, scryptR, scryptP, keyLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	plaintext, err := marshalSecrets(data)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(magic)+saltLen+nonceLen+len(plaintext)+16)
	out = append(out, magic...)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, nil)
	return out, nil
}

// decrypt opens a secret store produced by encrypt.
func decrypt(blob []byte, passphrase []byte) (map[string]string, error) {
	need := len(magic) + saltLen + nonceLen + 1
	if len(blob) < need || string(blob[:len(magic)]) != string(magic) {
		return nil, errors.New("not an merapi secret store")
	}
	salt := blob[len(magic) : len(magic)+saltLen]
	nonce := blob[len(magic)+saltLen : len(magic)+saltLen+nonceLen]
	ciphertext := blob[len(magic)+saltLen+nonceLen:]
	key, err := scrypt.Key(passphrase, salt, scryptN, scryptR, scryptP, keyLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("decryption failed")
	}
	return unmarshalSecrets(plaintext)
}

// The store payload is a simple length-prefixed format to avoid a JSON
// dependency round-trip and to keep ordering stable.
//
//	varint count
//	repeat count times:
//	  varint len(key) | key | varint len(value) | value
func marshalSecrets(data map[string]string) ([]byte, error) {
	var buf []byte
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sortStrings(keys)
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], uint64(len(keys)))
	buf = append(buf, tmp[:n]...)
	for _, k := range keys {
		v := data[k]
		n = binary.PutUvarint(tmp[:], uint64(len(k)))
		buf = append(buf, tmp[:n]...)
		buf = append(buf, k...)
		n = binary.PutUvarint(tmp[:], uint64(len(v)))
		buf = append(buf, tmp[:n]...)
		buf = append(buf, v...)
	}
	return buf, nil
}

func unmarshalSecrets(data []byte) (map[string]string, error) {
	out := map[string]string{}
	r := &byteReader{data: data}
	count, err := r.varint()
	if err != nil {
		return nil, err
	}
	for i := uint64(0); i < count; i++ {
		k, err := r.bytes()
		if err != nil {
			return nil, err
		}
		v, err := r.bytes()
		if err != nil {
			return nil, err
		}
		out[string(k)] = string(v)
	}
	return out, nil
}

type byteReader struct {
	data []byte
	pos  int
}

func (r *byteReader) varint() (uint64, error) {
	v, n := binary.Uvarint(r.data[r.pos:])
	if n <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	r.pos += n
	return v, nil
}

func (r *byteReader) bytes() ([]byte, error) {
	l, err := r.varint()
	if err != nil {
		return nil, err
	}
	end := r.pos + int(l)
	if end > len(r.data) {
		return nil, io.ErrUnexpectedEOF
	}
	b := r.data[r.pos:end]
	r.pos = end
	return b, nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
