package chainresource

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	ErrResourceAlreadyReserved = errors.New("chain resource already reserved")
	ErrInvalidResourceRequest  = errors.New("invalid chain resource request")
	ErrReservationFinalized    = errors.New("chain resource reservation already finalized")
)

type Manager struct {
	mu sync.Mutex

	activeNonces   map[string]nonceRecord
	consumedNonces map[string]uint64

	activeUTXOs   map[string]utxoRecord
	consumedUTXOs map[string]utxoRecord

	activeSequences map[string]sequenceRecord
}

func NewManager() *Manager {
	return &Manager{
		activeNonces:    map[string]nonceRecord{},
		consumedNonces:  map[string]uint64{},
		activeUTXOs:     map[string]utxoRecord{},
		consumedUTXOs:   map[string]utxoRecord{},
		activeSequences: map[string]sequenceRecord{},
	}
}

type NonceRequest struct {
	Chain   string
	Wallet  string
	Intent  string
	OwnerID string
}

type NonceReservation struct {
	manager *Manager
	key     string
	record  nonceRecord
	done    bool

	Nonce uint64
}

type nonceRecord struct {
	Chain   string
	Wallet  string
	Intent  string
	OwnerID string
	Nonce   uint64
	TxHash  string
}

func (m *Manager) ReserveNonce(ctx context.Context, req NonceRequest, fetch func(context.Context) (uint64, error)) (*NonceReservation, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: nil manager", ErrInvalidResourceRequest)
	}
	chain, wallet, owner := normalizeResourceIdentity(req.Chain, req.Wallet, req.OwnerID)
	if chain == "" || wallet == "" || owner == "" {
		return nil, fmt.Errorf("%w: chain, wallet and owner_id are required", ErrInvalidResourceRequest)
	}
	if fetch == nil {
		return nil, fmt.Errorf("%w: nonce fetcher is required", ErrInvalidResourceRequest)
	}
	baseNonce, err := fetch(ctx)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	nonce := baseNonce
	consumedKey := chain + ":" + wallet
	if next, ok := m.consumedNonces[consumedKey]; ok && nonce <= next {
		nonce = next + 1
	}
	key := nonceKey(chain, wallet, nonce)
	if _, ok := m.activeNonces[key]; ok {
		return nil, ErrResourceAlreadyReserved
	}

	record := nonceRecord{
		Chain:   chain,
		Wallet:  wallet,
		Intent:  strings.TrimSpace(req.Intent),
		OwnerID: owner,
		Nonce:   nonce,
	}
	m.activeNonces[key] = record
	return &NonceReservation{manager: m, key: key, record: record, Nonce: nonce}, nil
}

func (r *NonceReservation) Release() error {
	if r == nil || r.manager == nil {
		return fmt.Errorf("%w: nil nonce reservation", ErrInvalidResourceRequest)
	}
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	if r.done {
		return ErrReservationFinalized
	}
	delete(r.manager.activeNonces, r.key)
	r.done = true
	return nil
}

func (r *NonceReservation) Consume(txHash string) error {
	if r == nil || r.manager == nil {
		return fmt.Errorf("%w: nil nonce reservation", ErrInvalidResourceRequest)
	}
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	if r.done {
		return ErrReservationFinalized
	}
	delete(r.manager.activeNonces, r.key)
	consumedKey := r.record.Chain + ":" + r.record.Wallet
	if r.record.Nonce > r.manager.consumedNonces[consumedKey] {
		r.manager.consumedNonces[consumedKey] = r.record.Nonce
	}
	r.done = true
	return nil
}

type UTXO struct {
	TxID  string
	Vout  uint32
	Value int64
}

type UTXORequest struct {
	Chain   string
	Wallet  string
	Intent  string
	OwnerID string
	UTXOs   []UTXO
}

type UTXOReservation struct {
	manager *Manager
	keys    []string
	records []utxoRecord
	done    bool
}

type utxoRecord struct {
	Chain   string
	Wallet  string
	Intent  string
	OwnerID string
	UTXO    UTXO
	TxHash  string
}

func (m *Manager) ReserveUTXOs(ctx context.Context, req UTXORequest) (*UTXOReservation, error) {
	_ = ctx
	if m == nil {
		return nil, fmt.Errorf("%w: nil manager", ErrInvalidResourceRequest)
	}
	chain, wallet, owner := normalizeResourceIdentity(req.Chain, req.Wallet, req.OwnerID)
	if chain == "" || wallet == "" || owner == "" {
		return nil, fmt.Errorf("%w: chain, wallet and owner_id are required", ErrInvalidResourceRequest)
	}
	if len(req.UTXOs) == 0 {
		return nil, fmt.Errorf("%w: at least one UTXO is required", ErrInvalidResourceRequest)
	}

	keys := make([]string, 0, len(req.UTXOs))
	records := make([]utxoRecord, 0, len(req.UTXOs))
	seen := map[string]struct{}{}
	for _, utxo := range req.UTXOs {
		txID := strings.TrimSpace(utxo.TxID)
		if txID == "" {
			return nil, fmt.Errorf("%w: utxo txid is required", ErrInvalidResourceRequest)
		}
		key := utxoKey(chain, txID, utxo.Vout)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%w: duplicate UTXO in request", ErrInvalidResourceRequest)
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
		records = append(records, utxoRecord{
			Chain:   chain,
			Wallet:  wallet,
			Intent:  strings.TrimSpace(req.Intent),
			OwnerID: owner,
			UTXO:    UTXO{TxID: txID, Vout: utxo.Vout, Value: utxo.Value},
		})
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range keys {
		if _, ok := m.activeUTXOs[key]; ok {
			return nil, ErrResourceAlreadyReserved
		}
		if _, ok := m.consumedUTXOs[key]; ok {
			return nil, ErrResourceAlreadyReserved
		}
	}
	for i, key := range keys {
		m.activeUTXOs[key] = records[i]
	}
	return &UTXOReservation{manager: m, keys: keys, records: records}, nil
}

func (r *UTXOReservation) Release() error {
	if r == nil || r.manager == nil {
		return fmt.Errorf("%w: nil utxo reservation", ErrInvalidResourceRequest)
	}
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	if r.done {
		return ErrReservationFinalized
	}
	for _, key := range r.keys {
		delete(r.manager.activeUTXOs, key)
	}
	r.done = true
	return nil
}

func (r *UTXOReservation) Consume(txHash string) error {
	if r == nil || r.manager == nil {
		return fmt.Errorf("%w: nil utxo reservation", ErrInvalidResourceRequest)
	}
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	if r.done {
		return ErrReservationFinalized
	}
	for i, key := range r.keys {
		record := r.records[i]
		record.TxHash = strings.TrimSpace(txHash)
		delete(r.manager.activeUTXOs, key)
		r.manager.consumedUTXOs[key] = record
	}
	r.done = true
	return nil
}

type SequenceRequest struct {
	Chain   string
	Wallet  string
	Intent  string
	OwnerID string
}

type SequenceLease struct {
	manager *Manager
	key     string
	record  sequenceRecord
	done    bool
}

type sequenceRecord struct {
	Chain   string
	Wallet  string
	Intent  string
	OwnerID string
	TxHash  string
}

func (m *Manager) AcquireSequence(ctx context.Context, req SequenceRequest) (*SequenceLease, error) {
	_ = ctx
	if m == nil {
		return nil, fmt.Errorf("%w: nil manager", ErrInvalidResourceRequest)
	}
	chain, wallet, owner := normalizeResourceIdentity(req.Chain, req.Wallet, req.OwnerID)
	if chain == "" || wallet == "" || owner == "" {
		return nil, fmt.Errorf("%w: chain, wallet and owner_id are required", ErrInvalidResourceRequest)
	}
	key := sequenceKey(chain, wallet)

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.activeSequences[key]; ok {
		return nil, ErrResourceAlreadyReserved
	}
	record := sequenceRecord{
		Chain:   chain,
		Wallet:  wallet,
		Intent:  strings.TrimSpace(req.Intent),
		OwnerID: owner,
	}
	m.activeSequences[key] = record
	return &SequenceLease{manager: m, key: key, record: record}, nil
}

func (r *SequenceLease) Release() error {
	if r == nil || r.manager == nil {
		return fmt.Errorf("%w: nil sequence lease", ErrInvalidResourceRequest)
	}
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	if r.done {
		return ErrReservationFinalized
	}
	delete(r.manager.activeSequences, r.key)
	r.done = true
	return nil
}

func (r *SequenceLease) Consume(txHash string) error {
	if r == nil || r.manager == nil {
		return fmt.Errorf("%w: nil sequence lease", ErrInvalidResourceRequest)
	}
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	if r.done {
		return ErrReservationFinalized
	}
	delete(r.manager.activeSequences, r.key)
	r.done = true
	return nil
}

func normalizeResourceIdentity(chain, wallet, owner string) (string, string, string) {
	return strings.ToLower(strings.TrimSpace(chain)), strings.ToLower(strings.TrimSpace(wallet)), strings.TrimSpace(owner)
}

func nonceKey(chain, wallet string, nonce uint64) string {
	return fmt.Sprintf("%s:%s:nonce:%d", chain, wallet, nonce)
}

func utxoKey(chain, txID string, vout uint32) string {
	return fmt.Sprintf("%s:%s:%d", chain, strings.ToLower(strings.TrimSpace(txID)), vout)
}

func sequenceKey(chain, wallet string) string {
	return fmt.Sprintf("%s:%s:sequence", chain, wallet)
}
