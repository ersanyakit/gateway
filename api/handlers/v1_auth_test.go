package handlers

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"core/helpers"
	"core/models"
	"core/types"

	fiber "github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type fakeV1DomainLookup struct {
	byKey    map[string]*models.Domain
	bySecret map[string]*models.Domain
}

func (f fakeV1DomainLookup) FindByAPIKey(params types.DomainParams) (*models.Domain, error) {
	if params.APIKey == nil {
		return nil, errors.New("api key is required")
	}
	domain := f.byKey[*params.APIKey]
	if domain == nil {
		return nil, errors.New("record not found")
	}
	return domain, nil
}

func (f fakeV1DomainLookup) FindByAPISecret(params types.DomainParams) (*models.Domain, error) {
	if params.APISecret == nil {
		return nil, errors.New("api secret is required")
	}
	domain := f.bySecret[*params.APISecret]
	if domain == nil {
		return nil, errors.New("record not found")
	}
	return domain, nil
}

func TestV1ResolveDomainAcceptsAPIKeyAndBearer(t *testing.T) {
	domain := &models.Domain{ID: uuid.New(), APIKey: "key-1"}
	lookup := fakeV1DomainLookup{byKey: map[string]*models.Domain{"key-1": domain}}

	app := fiber.New()
	app.Get("/probe", func(c fiber.Ctx) error {
		resolved, err := v1ResolveDomain(c, lookup)
		if err != nil {
			return err
		}
		if resolved.ID != domain.ID {
			t.Fatalf("resolved domain = %s, want %s", resolved.ID, domain.ID)
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	for name, configure := range map[string]func(*http.Request){
		"api-key": func(req *http.Request) { req.Header.Set("X-API-Key", "key-1") },
		"bearer":  func(req *http.Request) { req.Header.Set("Authorization", "Bearer key-1") },
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/probe", nil)
			configure(req)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != fiber.StatusNoContent {
				t.Fatalf("status = %d", resp.StatusCode)
			}
		})
	}
}

func TestV1ResolveSignedDomainRejectsReplay(t *testing.T) {
	v1SignedReplayGuard = newV1SignatureReplayGuard(2 * time.Minute)

	domain := &models.Domain{ID: uuid.New(), APIKey: "key-1"}
	lookup := fakeV1DomainLookup{
		byKey:    map[string]*models.Domain{"key-1": domain},
		bySecret: map[string]*models.Domain{"secret-1": domain},
	}
	body := []byte(`{"amount":"1"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	signature := helpers.GenerateRequestSignature("secret-1", http.MethodPost, "/probe", ts, body)

	app := fiber.New()
	app.Post("/probe", func(c fiber.Ctx) error {
		_, err := v1ResolveSignedDomain(c, lookup)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	req1 := httptest.NewRequest(http.MethodPost, "/probe", bytesReader(body))
	req1.Header.Set("X-API-Key", "key-1")
	req1.Header.Set("X-API-Secret", "secret-1")
	req1.Header.Set("X-Gateway-Timestamp", ts)
	req1.Header.Set("X-Gateway-Signature", "sha256="+signature)
	resp1, err := app.Test(req1)
	if err != nil {
		t.Fatalf("first app.Test: %v", err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != fiber.StatusNoContent {
		t.Fatalf("first status = %d", resp1.StatusCode)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/probe", bytesReader(body))
	req2.Header.Set("X-API-Key", "key-1")
	req2.Header.Set("X-API-Secret", "secret-1")
	req2.Header.Set("X-Gateway-Timestamp", ts)
	req2.Header.Set("X-Gateway-Signature", "sha256="+signature)
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("replay app.Test: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("replay status = %d, want %d", resp2.StatusCode, fiber.StatusUnauthorized)
	}
}

func TestV1ResolveSignedDomainRejectsModifiedPath(t *testing.T) {
	v1SignedReplayGuard = newV1SignatureReplayGuard(2 * time.Minute)

	domain := &models.Domain{ID: uuid.New(), APIKey: "key-1"}
	lookup := fakeV1DomainLookup{
		byKey:    map[string]*models.Domain{"key-1": domain},
		bySecret: map[string]*models.Domain{"secret-1": domain},
	}
	body := []byte(`{"amount":"1"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	signature := helpers.GenerateRequestSignature("secret-1", http.MethodPost, "/other", ts, body)

	app := fiber.New()
	app.Post("/probe", func(c fiber.Ctx) error {
		_, err := v1ResolveSignedDomain(c, lookup)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/probe", bytesReader(body))
	req.Header.Set("X-API-Key", "key-1")
	req.Header.Set("X-API-Secret", "secret-1")
	req.Header.Set("X-Gateway-Timestamp", ts)
	req.Header.Set("X-Gateway-Signature", "sha256="+signature)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}
}

func TestV1ResolveSignedDomainRejectsModifiedMethodAndBody(t *testing.T) {
	for name, tc := range map[string]struct {
		signMethod string
		signBody   []byte
		sendBody   []byte
	}{
		"method": {
			signMethod: http.MethodGet,
			signBody:   []byte(`{"amount":"1"}`),
			sendBody:   []byte(`{"amount":"1"}`),
		},
		"body": {
			signMethod: http.MethodPost,
			signBody:   []byte(`{"amount":"1"}`),
			sendBody:   []byte(`{"amount":"2"}`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			v1SignedReplayGuard = newV1SignatureReplayGuard(2 * time.Minute)

			domain := &models.Domain{ID: uuid.New(), APIKey: "key-1"}
			lookup := fakeV1DomainLookup{
				byKey:    map[string]*models.Domain{"key-1": domain},
				bySecret: map[string]*models.Domain{"secret-1": domain},
			}
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			signature := helpers.GenerateRequestSignature("secret-1", tc.signMethod, "/probe", ts, tc.signBody)

			app := fiber.New()
			app.Post("/probe", func(c fiber.Ctx) error {
				_, err := v1ResolveSignedDomain(c, lookup)
				if err != nil {
					return v1Err(c, fiber.StatusUnauthorized, err.Error())
				}
				return c.SendStatus(fiber.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodPost, "/probe", bytesReader(tc.sendBody))
			req.Header.Set("X-API-Key", "key-1")
			req.Header.Set("X-API-Secret", "secret-1")
			req.Header.Set("X-Gateway-Timestamp", ts)
			req.Header.Set("X-Gateway-Signature", "sha256="+signature)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != fiber.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
			}
		})
	}
}

func TestV1ResolveSignedDomainRejectsAPIKeySecretMismatchWithoutLeakingSecrets(t *testing.T) {
	v1SignedReplayGuard = newV1SignatureReplayGuard(2 * time.Minute)

	domainA := &models.Domain{ID: uuid.New(), APIKey: "key-1"}
	domainB := &models.Domain{ID: uuid.New(), APIKey: "key-2"}
	lookup := fakeV1DomainLookup{
		byKey:    map[string]*models.Domain{"key-1": domainA},
		bySecret: map[string]*models.Domain{"secret-2": domainB},
	}
	body := []byte(`{"amount":"1"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	signature := helpers.GenerateRequestSignature("secret-2", http.MethodPost, "/probe", ts, body)

	app := fiber.New()
	app.Post("/probe", func(c fiber.Ctx) error {
		_, err := v1ResolveSignedDomain(c, lookup)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/probe", bytesReader(body))
	req.Header.Set("X-API-Key", "key-1")
	req.Header.Set("X-API-Secret", "secret-2")
	req.Header.Set("X-Gateway-Timestamp", ts)
	req.Header.Set("X-Gateway-Signature", "sha256="+signature)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusUnauthorized)
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	bodyText := string(respBody)
	for _, sensitive := range []string{"secret-2", signature, string(body), domainB.ID.String()} {
		if strings.Contains(bodyText, sensitive) {
			t.Fatalf("response leaked sensitive value %q in %s", sensitive, bodyText)
		}
	}
}

func TestV1DomainScopedHandlerHidesOtherDomainResource(t *testing.T) {
	domainA := &models.Domain{ID: uuid.New(), APIKey: "key-1"}
	domainB := &models.Domain{ID: uuid.New(), APIKey: "key-2"}
	lookup := fakeV1DomainLookup{byKey: map[string]*models.Domain{"key-1": domainA, "key-2": domainB}}
	resourceOwners := map[string]uuid.UUID{"pay_1": domainB.ID}

	app := fiber.New()
	app.Get("/payments/:id", func(c fiber.Ctx) error {
		domain, err := v1ResolveDomain(c, lookup)
		if err != nil {
			return v1Err(c, fiber.StatusUnauthorized, err.Error())
		}
		resourceID := c.Params("id")
		if resourceOwners[resourceID] != domain.ID {
			return v1Err(c, fiber.StatusNotFound, "payment not found")
		}
		return c.SendStatus(fiber.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/payments/pay_1", nil)
	req.Header.Set("X-API-Key", "key-1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusNotFound)
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	bodyText := string(respBody)
	if !strings.Contains(bodyText, "payment not found") {
		t.Fatalf("response = %s, want not-found envelope", bodyText)
	}
	if strings.Contains(bodyText, domainB.ID.String()) || strings.Contains(bodyText, "pay_1") {
		t.Fatalf("response leaked cross-domain resource details: %s", bodyText)
	}
}

func bytesReader(b []byte) *bytes.Reader {
	return bytes.NewReader(b)
}
