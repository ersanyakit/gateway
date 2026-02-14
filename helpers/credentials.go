package helpers

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	LivePrefix  = "gw_live"
	TestPrefix  = "gw_test"
	SecretPref  = "gw_secret"
	TimeSkewSec = 30
)

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func GenerateAPIKey(env string) (keyID string, apiKey string, err error) {

	id, err := randomHex(6)
	if err != nil {
		return "", "", err
	}

	randomPart, err := randomHex(12)
	if err != nil {
		return "", "", err
	}

	prefix := LivePrefix
	if env == "test" {
		prefix = TestPrefix
	}

	apiKey = prefix + "_" + id + "_" + randomPart
	return id, apiKey, nil
}

func GenerateSecret() (string, error) {
	r, err := randomHex(32)
	if err != nil {
		return "", err
	}
	return SecretPref + "_" + r, nil
}

func HashSHA256(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func GenerateSignature(secret string, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifySignature(secret string, timestamp string, body []byte, received string) bool {

	expected := GenerateSignature(secret, timestamp, body)

	return subtle.ConstantTimeCompare(
		[]byte(expected),
		[]byte(received),
	) == 1
}

func ValidateTimestamp(ts string) error {

	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errors.New("invalid timestamp")
	}

	now := time.Now().Unix()

	if now-t > TimeSkewSec || t-now > TimeSkewSec {
		return errors.New("timestamp expired")
	}

	return nil
}

func ExtractKeyID(apiKey string) (string, error) {

	parts := strings.Split(apiKey, "_")
	if len(parts) < 3 {
		return "", errors.New("invalid api key format")
	}

	return parts[1], nil
}

func EncryptSecret(secret string) (string, error) {
	masterKey := os.Getenv("MASTER_KEY")
	if masterKey == "" {
		return "", errors.New("MASTER_KEY not set")
	}

	hash := sha256.Sum256([]byte(masterKey))
	key := hash[:]

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(secret), nil)

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}
