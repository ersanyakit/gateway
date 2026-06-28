package repositories

import (
	"context"
	"errors"
	"testing"

	"core/models"

	"github.com/google/uuid"
)

func TestOutboundPolicyRepoFindEffectiveUsesMostSpecificScope(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.OutboundPolicySetting{}); err != nil {
		t.Fatalf("automigrate outbound policy tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, _ := seedWithdrawalOwner(t, db)
	repo := NewOutboundPolicyRepo(db)

	if _, err := repo.Upsert(ctx, OutboundPolicyUpdate{MaxAmountRaw: "100", ActorEmail: "owner@example.com"}); err != nil {
		t.Fatalf("upsert global policy: %v", err)
	}
	if _, err := repo.Upsert(ctx, OutboundPolicyUpdate{
		Scope:        OutboundPolicyScope{MerchantID: &merchantID},
		MaxAmountRaw: "50",
		ActorEmail:   "owner@example.com",
	}); err != nil {
		t.Fatalf("upsert merchant policy: %v", err)
	}
	if _, err := repo.Upsert(ctx, OutboundPolicyUpdate{
		Scope:        OutboundPolicyScope{DomainID: &domainID, Chain: " Ethereum "},
		MaxAmountRaw: "25",
		ActorEmail:   "owner@example.com",
	}); err != nil {
		t.Fatalf("upsert domain chain policy: %v", err)
	}

	effective, err := repo.FindEffective(ctx, merchantID, &domainID, "ethereum", nil)
	if err != nil {
		t.Fatalf("find effective policy: %v", err)
	}
	if effective.MaxAmountRaw != "25" {
		t.Fatalf("effective max = %q, want domain chain max 25", effective.MaxAmountRaw)
	}
}

func TestOutboundPolicyRepoWhitelistNormalizeMatchAndToggle(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.Merchant{}, &models.Domain{}, &models.Wallet{}, &models.OutboundAddressWhitelist{}); err != nil {
		t.Fatalf("automigrate outbound whitelist tables: %v", err)
	}
	ctx := context.Background()
	merchantID, domainID, _ := seedWithdrawalOwner(t, db)
	repo := NewOutboundPolicyRepo(db)
	entry, err := repo.AddWhitelist(ctx, OutboundWhitelistCreate{
		Scope:      OutboundPolicyScope{Chain: "Ethereum"},
		Address:    "0xAllowed",
		Label:      "cold",
		ActorEmail: "security@example.com",
	})
	if err != nil {
		t.Fatalf("add whitelist: %v", err)
	}

	ok, err := repo.IsAddressWhitelisted(ctx, merchantID, &domainID, "ethereum", nil, "0xallowed")
	if err != nil {
		t.Fatalf("check whitelist: %v", err)
	}
	if !ok {
		t.Fatal("expected normalized global whitelist match")
	}
	ok, err = repo.IsAddressWhitelisted(ctx, merchantID, &domainID, "ethereum", nil, "0xother")
	if err != nil {
		t.Fatalf("check whitelist miss: %v", err)
	}
	if ok {
		t.Fatal("unexpected whitelist match for other address")
	}
	if err := repo.SetWhitelistActive(ctx, entry.ID, false, "security@example.com"); err != nil {
		t.Fatalf("toggle whitelist inactive: %v", err)
	}
	ok, err = repo.IsAddressWhitelisted(ctx, merchantID, &domainID, "ethereum", nil, "0xallowed")
	if err != nil {
		t.Fatalf("check inactive whitelist: %v", err)
	}
	if ok {
		t.Fatal("inactive whitelist entry must not match")
	}
}

func TestOutboundPolicyRepoRejectsInvalidRawLimits(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.OutboundPolicySetting{}); err != nil {
		t.Fatalf("automigrate outbound policy tables: %v", err)
	}
	_, err := NewOutboundPolicyRepo(db).Upsert(context.Background(), OutboundPolicyUpdate{MaxAmountRaw: "not-a-number"})
	if !errors.Is(err, ErrOutboundPolicyInvalidAmount) {
		t.Fatalf("invalid max amount err = %v, want ErrOutboundPolicyInvalidAmount", err)
	}
}

func TestActivityLogIsAppendOnly(t *testing.T) {
	db := openMoneyEventOutboxPostgresTestDB(t)
	if err := db.AutoMigrate(&models.ActivityLog{}); err != nil {
		t.Fatalf("automigrate activity logs: %v", err)
	}
	ctx := context.Background()
	log := &models.ActivityLog{
		ID:        uuid.New(),
		ActorType: "admin",
		Event:     "audit.test",
		Status:    "success",
	}
	if err := NewActivityLogRepo(db).Create(ctx, log); err != nil {
		t.Fatalf("create activity log: %v", err)
	}
	log.Status = "failed"
	if err := db.WithContext(ctx).Save(log).Error; !errors.Is(err, models.ErrActivityLogAppendOnly) {
		t.Fatalf("activity log update err = %v, want append-only error", err)
	}
	if err := db.WithContext(ctx).Delete(log).Error; !errors.Is(err, models.ErrActivityLogAppendOnly) {
		t.Fatalf("activity log delete err = %v, want append-only error", err)
	}
}
