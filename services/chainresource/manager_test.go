package chainresource

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestManagerReserveNonceBlocksConcurrentSameWalletAndReusesAfterPreBroadcastRelease(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()
	req := NonceRequest{
		Chain:   "ethereum",
		Wallet:  "0xabc",
		Intent:  "transfer.native",
		OwnerID: "withdrawal-1",
	}

	first, err := manager.ReserveNonce(ctx, req, func(context.Context) (uint64, error) {
		return 7, nil
	})
	if err != nil {
		t.Fatalf("reserve first nonce: %v", err)
	}
	if first.Nonce != 7 {
		t.Fatalf("first nonce = %d, want 7", first.Nonce)
	}

	_, err = manager.ReserveNonce(ctx, NonceRequest{
		Chain:   "ethereum",
		Wallet:  "0xabc",
		Intent:  "transfer.native",
		OwnerID: "withdrawal-2",
	}, func(context.Context) (uint64, error) {
		return 7, nil
	})
	if !errors.Is(err, ErrResourceAlreadyReserved) {
		t.Fatalf("second reserve err=%v, want ErrResourceAlreadyReserved", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("release first nonce: %v", err)
	}
	retry, err := manager.ReserveNonce(ctx, req, func(context.Context) (uint64, error) {
		return 7, nil
	})
	if err != nil {
		t.Fatalf("reserve retry nonce: %v", err)
	}
	if retry.Nonce != 7 {
		t.Fatalf("retry nonce = %d, want 7 after pre-broadcast release", retry.Nonce)
	}
	if err := retry.Consume("0xtx"); err != nil {
		t.Fatalf("consume retry nonce: %v", err)
	}

	next, err := manager.ReserveNonce(ctx, NonceRequest{
		Chain:   "ethereum",
		Wallet:  "0xabc",
		Intent:  "transfer.native",
		OwnerID: "withdrawal-3",
	}, func(context.Context) (uint64, error) {
		return 7, nil
	})
	if err != nil {
		t.Fatalf("reserve next nonce: %v", err)
	}
	if next.Nonce != 8 {
		t.Fatalf("next nonce = %d, want 8 after nonce 7 consumed", next.Nonce)
	}
}

func TestManagerReserveNonceDoesNotReturnDuplicateUnderConcurrency(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	nonces := make(chan uint64, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			<-start
			res, err := manager.ReserveNonce(ctx, NonceRequest{
				Chain:   "ethereum",
				Wallet:  "0xabc",
				Intent:  "transfer.native",
				OwnerID: owner,
			}, func(context.Context) (uint64, error) {
				return 12, nil
			})
			if err != nil {
				errs <- err
				return
			}
			nonces <- res.Nonce
		}(string(rune('a' + i)))
	}
	close(start)
	wg.Wait()
	close(errs)
	close(nonces)

	seen := map[uint64]bool{}
	for nonce := range nonces {
		if seen[nonce] {
			t.Fatalf("duplicate nonce returned: %d", nonce)
		}
		seen[nonce] = true
	}
	for err := range errs {
		if !errors.Is(err, ErrResourceAlreadyReserved) {
			t.Fatalf("unexpected concurrent reserve error: %v", err)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("successful reservations = %d, want exactly one active nonce reservation", len(seen))
	}
}

func TestManagerReserveUTXOsBlocksActiveAndConsumedOutpoints(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()
	utxos := []UTXO{{TxID: "tx1", Vout: 0, Value: 10_000}}

	first, err := manager.ReserveUTXOs(ctx, UTXORequest{
		Chain:   "bitcoin",
		Wallet:  "bc1wallet",
		Intent:  "transfer.native",
		OwnerID: "withdrawal-1",
		UTXOs:   utxos,
	})
	if err != nil {
		t.Fatalf("reserve first utxo: %v", err)
	}

	_, err = manager.ReserveUTXOs(ctx, UTXORequest{
		Chain:   "bitcoin",
		Wallet:  "bc1wallet",
		Intent:  "transfer.native",
		OwnerID: "withdrawal-2",
		UTXOs:   utxos,
	})
	if !errors.Is(err, ErrResourceAlreadyReserved) {
		t.Fatalf("second utxo reserve err=%v, want ErrResourceAlreadyReserved", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("release first utxo: %v", err)
	}
	retry, err := manager.ReserveUTXOs(ctx, UTXORequest{
		Chain:   "bitcoin",
		Wallet:  "bc1wallet",
		Intent:  "transfer.native",
		OwnerID: "withdrawal-1",
		UTXOs:   utxos,
	})
	if err != nil {
		t.Fatalf("reserve after release: %v", err)
	}
	if err := retry.Consume("btctx"); err != nil {
		t.Fatalf("consume utxo: %v", err)
	}

	_, err = manager.ReserveUTXOs(ctx, UTXORequest{
		Chain:   "bitcoin",
		Wallet:  "bc1wallet",
		Intent:  "transfer.native",
		OwnerID: "withdrawal-3",
		UTXOs:   utxos,
	})
	if !errors.Is(err, ErrResourceAlreadyReserved) {
		t.Fatalf("reserve consumed utxo err=%v, want ErrResourceAlreadyReserved", err)
	}
}

func TestManagerSequenceLeaseSerializesWalletResource(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()

	first, err := manager.AcquireSequence(ctx, SequenceRequest{
		Chain:   "solana",
		Wallet:  "wallet-1",
		Intent:  "transfer.native",
		OwnerID: "sweep-1",
	})
	if err != nil {
		t.Fatalf("acquire first sequence: %v", err)
	}
	_, err = manager.AcquireSequence(ctx, SequenceRequest{
		Chain:   "solana",
		Wallet:  "wallet-1",
		Intent:  "transfer.native",
		OwnerID: "sweep-2",
	})
	if !errors.Is(err, ErrResourceAlreadyReserved) {
		t.Fatalf("second sequence err=%v, want ErrResourceAlreadyReserved", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release first sequence: %v", err)
	}
	second, err := manager.AcquireSequence(ctx, SequenceRequest{
		Chain:   "solana",
		Wallet:  "wallet-1",
		Intent:  "transfer.native",
		OwnerID: "sweep-2",
	})
	if err != nil {
		t.Fatalf("acquire second after release: %v", err)
	}
	if err := second.Consume("signature"); err != nil {
		t.Fatalf("consume sequence: %v", err)
	}
}
