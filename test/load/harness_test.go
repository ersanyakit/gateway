package loadtest

import (
	"context"
	"testing"
)

func TestQueueLoadHarnessRecordsThroughputLatencyAndBacklog(t *testing.T) {
	scenarios := []Scenario{
		{Name: "webhook-delivery", Items: 256, Workers: 8},
		{Name: "deposit-facts", Items: 256, Workers: 4},
		{Name: "sweep-outbound", Items: 128, Workers: 4},
	}

	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			report, err := RunScenario(context.Background(), scenario)
			if err != nil {
				t.Fatal(err)
			}
			if report.ThroughputPerSecond <= 0 {
				t.Fatalf("throughput = %f, want positive", report.ThroughputPerSecond)
			}
			if report.Backlog != 0 || report.Errors != 0 {
				t.Fatalf("report backlog/errors = %d/%d, want zero", report.Backlog, report.Errors)
			}
			if report.P95LatencyMS < report.P50LatencyMS {
				t.Fatalf("latency p95=%f p50=%f", report.P95LatencyMS, report.P50LatencyMS)
			}
		})
	}
}
