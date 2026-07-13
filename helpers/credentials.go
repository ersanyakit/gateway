package helpers

import (
	"context"
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

func GenerateRequestSignature(secret string, method string, path string, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.ToUpper(strings.TrimSpace(method))))
	mac.Write([]byte("\n"))
	mac.Write([]byte(strings.TrimSpace(path)))
	mac.Write([]byte("\n"))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyRequestSignature(secret string, method string, path string, timestamp string, body []byte, received string) bool {
	expected := GenerateRequestSignature(secret, method, path, timestamp, body)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(received)) == 1
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
	if len(parts) < 4 {
		return "", errors.New("invalid api key format")
	}
	if parts[0] != "gw" || (parts[1] != "live" && parts[1] != "test") || parts[2] == "" {
		return "", errors.New("invalid api key format")
	}

	return parts[2], nil
}

func ValidateDomainHost(rawHost string) error {
	host := strings.TrimSpace(strings.ToLower(rawHost))
	if host == "" {
		return errors.New("domain host is required")
	}
	if strings.Contains(host, "://") {
		return errors.New("domain host must not include a scheme")
	}
	if strings.ContainsAny(host, "/?#@") {
		return errors.New("domain host must not include path, query, fragment, or credentials")
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return errors.New("domain host is malformed")
	}
	if len(host) > 253 {
		return errors.New("domain host is too long")
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return errors.New("domain host must include a public suffix")
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return errors.New("domain host label is malformed")
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return errors.New("domain host label must not start or end with hyphen")
		}
		for _, ch := range label {
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
				continue
			}
			return errors.New("domain host must contain only letters, numbers, dots, and hyphens")
		}
	}
	return nil
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

func isPrivateIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return true
	}

	// net.IP.IsPrivate intentionally excludes the shared address space used for
	// carrier-grade NAT (RFC 6598), but it is not a safe public webhook/NATS
	// destination either. Match 100.64.0.0/10 directly so package
	// initialization does not need a must-style CIDR parser.
	ipv4 := ip.To4()
	return ipv4 != nil && ipv4[0] == 100 && ipv4[1]&0xc0 == 64
}

func allowPrivateWebhookURLs() bool {
	appEnv := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if appEnv == "production" {
		return false
	}
	if v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("ALLOW_PRIVATE_WEBHOOK_URLS"))); err == nil && v {
		return true
	}
	return appEnv == "development" || appEnv == "dev" || appEnv == "local" || appEnv == "test"
}

func allowPrivateNATSURLs() bool {
	appEnv := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if appEnv == "production" {
		return false
	}
	if v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("ALLOW_PRIVATE_NATS_URLS"))); err == nil && v {
		return true
	}
	return appEnv == "development" || appEnv == "dev" || appEnv == "local" || appEnv == "test"
}

func resolveHostIPs(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		ips = append(ips, address.IP)
	}
	return ips, nil
}

func ValidateWebhookURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid webhook url: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("webhook url must use http or https scheme")
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") && u.Scheme != "https" {
		return errors.New("production webhook url must use https scheme")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("webhook url must have a host")
	}
	ips, err := resolveHostIPs(host)
	if err != nil {
		return fmt.Errorf("webhook url host lookup failed: %w", err)
	}
	for _, ip := range ips {
		if isPrivateIP(ip) && !allowPrivateWebhookURLs() {
			return errors.New("webhook url must not resolve to a private or loopback address")
		}
	}
	return nil
}

// ValidateNATSURL validates the server address used for merchant event
// publishing. NATS subjects are stored separately so a merchant can route
// events without putting wildcard syntax into the server URL.
func ValidateNATSURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return errors.New("nats URL is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid nats URL: %w", err)
	}
	host := parsed.Hostname()
	if host == "" {
		return errors.New("nats URL must include a host")
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "nats", "tls":
	default:
		return errors.New("nats URL must use the nats or tls scheme")
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") && scheme != "tls" {
		return errors.New("production nats URL must use the tls scheme")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("nats URL must not include a path, query, or fragment")
	}
	if parsed.User != nil {
		return errors.New("nats URL must not contain inline credentials")
	}
	ips, err := resolveHostIPs(host)
	if err != nil {
		return fmt.Errorf("nats URL host lookup failed: %w", err)
	}
	for _, ip := range ips {
		if isPrivateIP(ip) && !allowPrivateNATSURLs() {
			return errors.New("nats URL must not resolve to a private or loopback address")
		}
	}
	return nil
}

func ValidateNATSSubject(subject string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return nil
	}
	if strings.ContainsAny(subject, " \t\r\n>*") {
		return errors.New("nats subject contains invalid characters")
	}
	for _, part := range strings.Split(subject, ".") {
		if part == "" {
			return errors.New("nats subject contains an empty token")
		}
	}
	return nil
}
