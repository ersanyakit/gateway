package docs

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type prometheusRuleFile struct {
	Groups []prometheusRuleGroup `yaml:"groups"`
}

type prometheusRuleGroup struct {
	Name  string            `yaml:"name"`
	Rules []prometheusAlert `yaml:"rules"`
}

type prometheusAlert struct {
	Alert       string            `yaml:"alert"`
	Expr        string            `yaml:"expr"`
	For         string            `yaml:"for"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
}

func TestGatewayPrometheusAlertRulesAreValidAndCoverMoneyPath(t *testing.T) {
	raw, err := os.ReadFile("../deploy/prometheus/gateway-alerts.yaml")
	if err != nil {
		t.Fatalf("read alert rules: %v", err)
	}
	var rules prometheusRuleFile
	if err := yaml.Unmarshal(raw, &rules); err != nil {
		t.Fatalf("parse alert rules: %v", err)
	}
	if len(rules.Groups) == 0 {
		t.Fatal("alert rules must define at least one group")
	}

	seenMetrics := map[string]bool{}
	seenAlerts := map[string]bool{}
	for _, group := range rules.Groups {
		if strings.TrimSpace(group.Name) == "" || len(group.Rules) == 0 {
			t.Fatalf("invalid alert group: %#v", group)
		}
		for _, rule := range group.Rules {
			if strings.TrimSpace(rule.Alert) == "" || strings.TrimSpace(rule.Expr) == "" || strings.TrimSpace(rule.For) == "" {
				t.Fatalf("alert missing required fields: %#v", rule)
			}
			if rule.Labels["severity"] == "" || rule.Annotations["runbook_url"] == "" || rule.Annotations["summary"] == "" {
				t.Fatalf("alert missing severity/runbook/summary: %#v", rule)
			}
			seenAlerts[rule.Alert] = true
			for _, metric := range []string{
				"gateway_chain_state_age_seconds",
				"gateway_provider_lag_blocks",
				"gateway_webhook_delivery_backlog",
				"gateway_production_signer_ready",
				"gateway_signer_adapter_ready",
				"gateway_sweep_job_backlog",
				"gateway_reconciliation_jobs",
			} {
				if strings.Contains(rule.Expr, metric) {
					seenMetrics[metric] = true
				}
			}
		}
	}
	for _, alert := range []string{
		"GatewayChainStateStale",
		"GatewayProviderLag",
		"GatewayWebhookBacklog",
		"GatewayWebhookDeadLetters",
		"GatewaySignerNotReady",
		"GatewaySweepDeadLetters",
		"GatewayReconciliationDrift",
	} {
		if !seenAlerts[alert] {
			t.Fatalf("alert %s missing", alert)
		}
	}
	for _, metric := range []string{
		"gateway_chain_state_age_seconds",
		"gateway_provider_lag_blocks",
		"gateway_webhook_delivery_backlog",
		"gateway_production_signer_ready",
		"gateway_signer_adapter_ready",
		"gateway_sweep_job_backlog",
		"gateway_reconciliation_jobs",
	} {
		if !seenMetrics[metric] {
			t.Fatalf("metric %s is not covered by alert expressions", metric)
		}
	}
	for _, forbidden := range []string{"api_secret", "webhook_secret", "private_key", "mnemonic", "raw_signature"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("alert rules leaked forbidden token %q", forbidden)
		}
	}
}
