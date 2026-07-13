package loadtest

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

type Scenario struct {
	Name    string
	Items   int
	Workers int
	Handler func(context.Context, int) error
}

type Report struct {
	Name                string
	Items               int
	Workers             int
	DurationMS          float64
	ThroughputPerSecond float64
	P50LatencyMS        float64
	P95LatencyMS        float64
	Backlog             int
	Errors              int
}

func RunScenario(ctx context.Context, scenario Scenario) (Report, error) {
	if scenario.Items <= 0 {
		return Report{}, errors.New("load scenario requires at least one item")
	}
	if scenario.Workers <= 0 {
		scenario.Workers = 1
	}
	if scenario.Handler == nil {
		scenario.Handler = func(context.Context, int) error { return nil }
	}

	started := time.Now()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	latencies := make([]time.Duration, 0, scenario.Items)
	errorCount := 0

	for worker := 0; worker < scenario.Workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				itemStarted := time.Now()
				err := scenario.Handler(ctx, item)
				elapsed := time.Since(itemStarted)
				mu.Lock()
				latencies = append(latencies, elapsed)
				if err != nil {
					errorCount++
				}
				mu.Unlock()
			}
		}()
	}

submitted:
	for item := 0; item < scenario.Items; item++ {
		select {
		case <-ctx.Done():
			break submitted
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()

	mu.Lock()
	processed := len(latencies)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	errorsSeen := errorCount
	mu.Unlock()

	duration := time.Since(started)
	durationSeconds := duration.Seconds()
	throughput := 0.0
	if durationSeconds > 0 {
		throughput = float64(processed) / durationSeconds
	}
	return Report{
		Name:                scenario.Name,
		Items:               scenario.Items,
		Workers:             scenario.Workers,
		DurationMS:          duration.Seconds() * 1000,
		ThroughputPerSecond: throughput,
		P50LatencyMS:        percentileMS(latencies, 0.50),
		P95LatencyMS:        percentileMS(latencies, 0.95),
		Backlog:             scenario.Items - processed,
		Errors:              errorsSeen,
	}, nil
}

func percentileMS(values []time.Duration, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if percentile <= 0 {
		return durationMS(values[0])
	}
	index := int(float64(len(values)-1) * percentile)
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return durationMS(values[index])
}

func durationMS(value time.Duration) float64 {
	return float64(value.Nanoseconds()) / float64(time.Millisecond)
}
