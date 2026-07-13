package models

import (
	"core/constants"
	"time"

	"github.com/google/uuid"
)

const (
	ProviderHealthStatusHealthy   = "healthy"
	ProviderHealthStatusDegraded  = "degraded"
	ProviderHealthStatusUnhealthy = "unhealthy"
	ProviderHealthStatusUnknown   = "unknown"
)

type ProviderHealthSnapshot struct {
	ID                  uuid.UUID         `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	ChainID             constants.ChainID `gorm:"type:bigint;not null;uniqueIndex:ux_provider_health_chain_provider;index" json:"chain_id"`
	ChainName           string            `gorm:"size:64;not null;index" json:"chain_name"`
	ProviderLabel       string            `gorm:"size:128;not null" json:"provider_label"`
	ProviderURLHash     string            `gorm:"size:64;not null;uniqueIndex:ux_provider_health_chain_provider;index" json:"provider_url_hash"`
	Reachable           bool              `gorm:"not null;default:false" json:"reachable"`
	Status              string            `gorm:"size:24;not null;default:'unknown';index" json:"status"`
	LatestHeight        int64             `gorm:"not null;default:0" json:"latest_height"`
	HeadHash            string            `gorm:"size:128" json:"head_hash"`
	ResponseLatencyMS   int64             `gorm:"not null;default:0" json:"response_latency_ms"`
	LagFromReference    int64             `gorm:"not null;default:0" json:"lag_from_reference"`
	ErrorCategory       string            `gorm:"size:64;index" json:"error_category"`
	ErrorDetail         string            `gorm:"size:512" json:"error_detail"`
	Selected            bool              `gorm:"not null;default:false;index" json:"selected"`
	FailoverReason      string            `gorm:"size:128" json:"failover_reason"`
	ConsecutiveFailures int               `gorm:"not null;default:0" json:"consecutive_failures"`
	CheckedAt           time.Time         `gorm:"not null;index" json:"checked_at"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}
