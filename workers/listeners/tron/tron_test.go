package tron

import (
	"context"
	"errors"
	"testing"
)

func TestLatestBlockNumberWithoutClientReturnsError(t *testing.T) {
	listener := &RpcListener{}

	_, err := listener.latestBlockNumber(context.Background())
	if !errors.Is(err, errTronClientNotConnected) {
		t.Fatalf("expected not connected error, got %v", err)
	}
}

func TestWalletClientNilReceiverReturnsError(t *testing.T) {
	var client *walletClient

	_, err := client.getNowBlock(context.Background())
	if !errors.Is(err, errTronClientNotConnected) {
		t.Fatalf("expected not connected error, got %v", err)
	}
}
