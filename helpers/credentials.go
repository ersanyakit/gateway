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
	"fmt"
	"io"
	"net"
	"net/url"
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

func GenerateBcryptSafeSecret() (string, error) {
	r, err := randomHex(24)
	if err != nil {
		return "", err
	}
	return SecretPref + "_" + r, nil
}

func HashSHA256(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

// HMACSecret computes HMAC-SHA256(MASTER_KEY, secret) — deterministic one-way hash
// suitable for DB lookup without revealing the plaintext. Use this for storing API
// secrets that must be looked up by value (not decrypted).
func HMACSecret(secret string) (string, error) {
	masterKey := os.Getenv("MASTER_KEY")
	if masterKey == "" {
		return "", errors.New("MASTER_KEY not set")
	}
	mac := hmac.New(sha256.New, []byte(masterKey))
	mac.Write([]byte(secret))
	return hex.EncodeToString(mac.Sum(nil)), nil
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

func DecryptSecret(encrypted string) (string, error) {
	masterKey := os.Getenv("MASTER_KEY")
	if masterKey == "" {
		return "", errors.New("MASTER_KEY not set")
	}

	hash := sha256.Sum256([]byte(masterKey))
	key := hash[:]

	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

var privateRanges = []*net.IPNet{
	mustParseCIDR("10.0.0.0/8"),
	mustParseCIDR("172.16.0.0/12"),
	mustParseCIDR("192.168.0.0/16"),
	mustParseCIDR("127.0.0.0/8"),
	mustParseCIDR("169.254.0.0/16"),
	mustParseCIDR("100.64.0.0/10"),
	mustParseCIDR("::1/128"),
	mustParseCIDR("fc00::/7"),
}

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func isPrivateIP(ip net.IP) bool {
	for _, r := range privateRanges {
		if r.Contains(ip) {
			return true
		}
	}
	return false
}

func ValidateWebhookURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid webhook url: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("webhook url must use http or https scheme")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("webhook url must have a host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("webhook url host lookup failed: %w", err)
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return errors.New("webhook url must not resolve to a private or loopback address")
		}
	}
	return nil
}
