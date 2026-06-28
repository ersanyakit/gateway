package middleware

import (
	"crypto/hmac"
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
	portalJWTTokenCookie = "gateway_portal_jwt"
	portalJWTTokenHeader = "X-Portal-JWT"
	portalJWTTokenForm   = "_portal_jwt"
	portalJWTTokenTTL    = 2 * time.Hour
)

var runtimePortalJWTSecret = "runtime-portal-jwt-secret-" + uuid.NewString()

type portalJWTClaims struct {
	Iss         string `json:"iss"`
	Typ         string `json:"typ"`
	JTI         string `json:"jti"`
	Iat         int64  `json:"iat"`
	Exp         int64  `json:"exp"`
	SessionHash string `json:"session_hash"`
}

func PortalMutationJWT() fiber.Handler {
	return func(c fiber.Ctx) error {
		if isPortalJWTSafeMethod(c.Method()) {
			setPortalJWTTokenCookie(c)
			return c.Next()
		}

		token := strings.TrimSpace(c.Get(portalJWTTokenHeader))
		if token == "" {
			token = strings.TrimSpace(c.FormValue(portalJWTTokenForm))
		}
		if err := verifyPortalJWT(token, currentPortalJWTSessionHash(c), time.Now()); err != nil {
			return c.Status(fiber.StatusForbidden).SendString("invalid portal jwt")
		}
		setPortalJWTTokenCookie(c)
		return c.Next()
	}
}

func isPortalJWTSafeMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions, "TRACE":
		return true
	default:
		return false
	}
}

func setPortalJWTTokenCookie(c fiber.Ctx) {
	now := time.Now()
	token := signPortalJWT(portalJWTClaims{
		Iss:         "gateway",
		Typ:         "portal_mutation",
		JTI:         uuid.NewString(),
		Iat:         now.Unix(),
		Exp:         now.Add(portalJWTTokenTTL).Unix(),
		SessionHash: currentPortalJWTSessionHash(c),
	})
	c.Cookie(&fiber.Cookie{
		Name:     portalJWTTokenCookie,
		Value:    token,
		Path:     "/",
		HTTPOnly: false,
		SameSite: "Lax",
		MaxAge:   int(portalJWTTokenTTL.Seconds()),
		Expires:  now.Add(portalJWTTokenTTL),
		Secure:   requestIsHTTPS(c),
	})
}

func signPortalJWT(claims portalJWTClaims) string {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerRaw, _ := json.Marshal(header)
	claimsRaw, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(headerRaw) + "." + base64.RawURLEncoding.EncodeToString(claimsRaw)
	mac := hmac.New(sha256.New, []byte(portalJWTSecret()))
	mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verifyPortalJWT(token string, sessionHash string, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return errors.New("invalid portal jwt")
	}
	unsigned := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(portalJWTSecret()))
	mac.Write([]byte(unsigned))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return errors.New("invalid portal jwt signature")
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
		return errors.New("invalid portal jwt header")
	}
	claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return err
	}
	var claims portalJWTClaims
	if err := json.Unmarshal(claimsRaw, &claims); err != nil {
		return err
	}
	if claims.Iss != "gateway" || claims.Typ != "portal_mutation" || claims.JTI == "" {
		return errors.New("invalid portal jwt claims")
	}
	if claims.Exp <= 0 || !now.Before(time.Unix(claims.Exp, 0)) {
		return errors.New("expired portal jwt")
	}
	if claims.Iat <= 0 || time.Unix(claims.Iat, 0).After(now.Add(time.Minute)) {
		return errors.New("invalid portal jwt issued-at")
	}
	if !hmac.Equal([]byte(claims.SessionHash), []byte(sessionHash)) {
		return errors.New("portal jwt session mismatch")
	}
	return nil
}

func currentPortalJWTSessionHash(c fiber.Ctx) string {
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

func portalJWTSecret() string {
	for _, key := range []string{"PORTAL_JWT_SECRET", "DEALER_SESSION_SECRET", "SESSION_SECRET", "MASTER_KEY"} {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return runtimePortalJWTSecret
}
