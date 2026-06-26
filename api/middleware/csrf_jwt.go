package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const (
	csrfTokenCookie = "gateway_csrf_jwt"
	csrfSeedCookie  = "gateway_csrf_seed"
	csrfTokenHeader = "X-CSRF-Token"
	csrfTokenForm   = "_csrf"
	csrfTokenTTL    = 2 * time.Hour
)

var runtimeCSRFSecret = "runtime-csrf-secret-" + uuid.NewString()

type csrfJWTClaims struct {
	Iss         string `json:"iss"`
	Typ         string `json:"typ"`
	JTI         string `json:"jti"`
	Iat         int64  `json:"iat"`
	Exp         int64  `json:"exp"`
	SeedHash    string `json:"seed_hash"`
	SessionHash string `json:"session_hash"`
}

func PortalCSRF() fiber.Handler {
	return func(c fiber.Ctx) error {
		seed := ensureCSRFSeed(c)
		if isCSRFSafeMethod(c.Method()) {
			setCSRFTokenCookie(c, seed)
			return c.Next()
		}

		token := strings.TrimSpace(c.Get(csrfTokenHeader))
		if token == "" {
			token = strings.TrimSpace(c.FormValue(csrfTokenForm))
		}
		if err := verifyCSRFJWT(token, seed, currentCSRFSessionHash(c), time.Now()); err != nil {
			return c.Status(fiber.StatusForbidden).SendString("invalid csrf token")
		}
		setCSRFTokenCookie(c, seed)
		return c.Next()
	}
}

func isCSRFSafeMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions, "TRACE":
		return true
	default:
		return false
	}
}

func ensureCSRFSeed(c fiber.Ctx) string {
	seed := strings.TrimSpace(c.Cookies(csrfSeedCookie))
	if seed == "" {
		seed = randomURLToken(32)
	}
	c.Cookie(&fiber.Cookie{
		Name:     csrfSeedCookie,
		Value:    seed,
		Path:     "/",
		HTTPOnly: true,
		SameSite: "Lax",
		MaxAge:   int((24 * time.Hour).Seconds()),
		Expires:  time.Now().Add(24 * time.Hour),
		Secure:   requestIsHTTPS(c),
	})
	return seed
}

func setCSRFTokenCookie(c fiber.Ctx, seed string) {
	now := time.Now()
	token := signCSRFJWT(csrfJWTClaims{
		Iss:         "gateway",
		Typ:         "csrf",
		JTI:         uuid.NewString(),
		Iat:         now.Unix(),
		Exp:         now.Add(csrfTokenTTL).Unix(),
		SeedHash:    sha256Hex(seed),
		SessionHash: currentCSRFSessionHash(c),
	})
	c.Cookie(&fiber.Cookie{
		Name:     csrfTokenCookie,
		Value:    token,
		Path:     "/",
		HTTPOnly: false,
		SameSite: "Lax",
		MaxAge:   int(csrfTokenTTL.Seconds()),
		Expires:  now.Add(csrfTokenTTL),
		Secure:   requestIsHTTPS(c),
	})
}

func signCSRFJWT(claims csrfJWTClaims) string {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerRaw, _ := json.Marshal(header)
	claimsRaw, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(headerRaw) + "." + base64.RawURLEncoding.EncodeToString(claimsRaw)
	mac := hmac.New(sha256.New, []byte(csrfSecret()))
	mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verifyCSRFJWT(token string, seed string, sessionHash string, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return errors.New("invalid csrf token")
	}
	unsigned := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(csrfSecret()))
	mac.Write([]byte(unsigned))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return errors.New("invalid csrf signature")
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	var header map[string]string
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return err
	}
	if !strings.EqualFold(header["alg"], "HS256") || !strings.EqualFold(header["typ"], "JWT") {
		return errors.New("invalid csrf header")
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	var claims csrfJWTClaims
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		return err
	}
	if claims.Iss != "gateway" || claims.Typ != "csrf" || claims.JTI == "" {
		return errors.New("invalid csrf claims")
	}
	if claims.Exp <= 0 || !now.Before(time.Unix(claims.Exp, 0)) {
		return errors.New("expired csrf token")
	}
	if claims.Iat <= 0 || time.Unix(claims.Iat, 0).After(now.Add(time.Minute)) {
		return errors.New("invalid csrf issued-at")
	}
	if !hmac.Equal([]byte(claims.SeedHash), []byte(sha256Hex(seed))) {
		return errors.New("csrf seed mismatch")
	}
	if !hmac.Equal([]byte(claims.SessionHash), []byte(sessionHash)) {
		return errors.New("csrf session mismatch")
	}
	return nil
}

func currentCSRFSessionHash(c fiber.Ctx) string {
	names := []string{
		"dealer_session",
		"admin_session",
		"admin_totp_pending",
		"admin_totp_setup",
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		value := strings.TrimSpace(c.Cookies(name))
		if value != "" {
			parts = append(parts, name+"="+value)
		}
	}
	return sha256Hex(strings.Join(parts, "|"))
}

func requestIsHTTPS(c fiber.Ctx) bool {
	return strings.EqualFold(c.Protocol(), "https") || strings.EqualFold(c.Get("X-Forwarded-Proto"), "https")
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomURLToken(size int) string {
	if size <= 0 {
		size = 32
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return uuid.NewString()
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func csrfSecret() string {
	for _, key := range []string{"CSRF_JWT_SECRET", "DEALER_SESSION_SECRET", "SESSION_SECRET", "MASTER_KEY"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return runtimeCSRFSecret
}
