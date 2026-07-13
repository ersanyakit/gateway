package blockchain

import (
	"context"
	"core/blockchain/walletcore"
	"core/constants"
	"core/helpers"
	"core/models"
	"core/services/signer"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
)

type RPCURLRanker func(chainID constants.ChainID, chainName string, urls []string) []string

var rpcURLRankerState struct {
	sync.RWMutex
	ranker RPCURLRanker
}

func SetRPCURLRanker(ranker RPCURLRanker) {
	rpcURLRankerState.Lock()
	defer rpcURLRankerState.Unlock()
	rpcURLRankerState.ranker = ranker
}

func rankRPCURLs(chainID constants.ChainID, chainName string, urls []string) []string {
	rpcURLRankerState.RLock()
	ranker := rpcURLRankerState.ranker
	rpcURLRankerState.RUnlock()
	if ranker == nil {
		return urls
	}
	ranked := ranker(chainID, chainName, urls)
	if len(ranked) == 0 {
		return urls
	}
	return ranked
}

type WalletDetails struct {
	Address         string `json:"address"`
	PrivateKey      string `json:"-"`
	MnemonicPhrase  string `json:"-"`
	KeyReference    string `json:"key_reference,omitempty"`
	DerivationPath  string `json:"derivation_path,omitempty"`
	SignerMode      string `json:"signer_mode,omitempty"`
	CustodyProvider string `json:"custody_provider,omitempty"`
	WatchOnly       bool   `json:"watch_only,omitempty"`
}

type TransactionResult struct {
	TxHash  string
	Success bool
	Error   error
}

type Worker interface {
	Start() error
	Stop() error
	Events() <-chan interface{}
}

var (
	ErrNilBaseChain                = errors.New("base chain is nil")
	ErrNilWorker                   = errors.New("blockchain worker is nil")
	ErrWorkerContextNotInitialized = errors.New("blockchain worker context is not initialized")
)

type Chain interface {
	ChainID() constants.ChainID
	Name() string
	WSS() []string
	RPCs() []string
	Create(ctx context.Context) (*WalletDetails, error)
	CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*WalletDetails, error)

	Deposit(ctx context.Context, wallet WalletDetails, amountRaw string, toAddress string) (*TransactionResult, error)
	Withdraw(ctx context.Context, wallet WalletDetails, amountRaw string, toAddress string) (*TransactionResult, error)
	WithdrawToken(ctx context.Context, wallet WalletDetails, tokenAddr string, amountRaw string, toAddress string) (*TransactionResult, error)
	Sweep(ctx context.Context, wallet WalletDetails) (*TransactionResult, error)
	SweepTo(ctx context.Context, wallet WalletDetails, toAddress string) (*TransactionResult, error)
	SweepERC20To(ctx context.Context, wallet WalletDetails, contractAddr, toAddress string) (*TransactionResult, error)
	PrefundGas(ctx context.Context, reserveWallet WalletDetails, userAddress string) (bool, error)
	ValidateAddress(address string) bool

	AddWorker(listener Worker) error
	RemoveWorker(listener Worker) error
	WorkerCount() int
	BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult
	StartWorkers(ctx context.Context) error
	StopWorkers() error
}

type BaseChain struct {
	ID          constants.ChainID
	ChainName   string
	ExplorerURL string
	RPCHttp     []string
	WebSockets  []string

	Workers []Worker

	ctx    context.Context
	cancel context.CancelFunc
}

func (b *BaseChain) Name() string {
	return b.ChainName
}

func (b *BaseChain) ChainID() constants.ChainID {
	return b.ID
}

func (b *BaseChain) RPCs() []string {
	rpcs := make([]string, 0, len(b.RPCHttp)+4)
	seen := make(map[string]struct{}, len(b.RPCHttp)+4)

	add := func(values []string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			rpcs = append(rpcs, value)
		}
	}

	for _, envName := range b.rpcEnvNames() {
		add(splitRPCEnv(os.Getenv(envName)))
	}
	add(splitRPCEnv(os.Getenv("CHAIN_" + strconv.FormatInt(int64(b.ID), 10) + "_RPC_URLS")))
	add(b.RPCHttp)

	return rankRPCURLs(b.ID, b.ChainName, rpcs)
}

func (b *BaseChain) Explorer() string {
	return b.ExplorerURL
}

func (b *BaseChain) WSS() []string {
	return b.WebSockets
}

func splitRPCEnv(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func verboseEventLoggingEnabled() bool {
	for _, key := range []string{"GATEWAY_VERBOSE_EVENTS", "VERBOSE"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "1", "true", "yes", "on", "verbose":
			return true
		}
	}
	return false
}

func (b *BaseChain) rpcEnvNames() []string {
	name := strings.ToUpper(strings.NewReplacer("-", "_").Replace(b.ChainName))
	names := []string{name + "_RPC_URLS"}

	switch b.ChainName {
	case "bnbchain":
		names = append(names, "BSC_RPC_URLS", "BINANCE_RPC_URLS")
	case "tron":
		names = append(names, "TRON_JSONRPC_URLS")
	case "tron-testnet":
		names = append(names, "TRON_TESTNET_JSONRPC_URLS")
	}

	return names
}

func (b *BaseChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId uint32) (*WalletDetails, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) Create(ctx context.Context) (*WalletDetails, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) Deposit(ctx context.Context, wallet WalletDetails, amountRaw string, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) Withdraw(ctx context.Context, wallet WalletDetails, amountRaw string, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) WithdrawToken(ctx context.Context, wallet WalletDetails, tokenAddr string, amountRaw string, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) Sweep(ctx context.Context, wallet WalletDetails) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) SweepTo(ctx context.Context, wallet WalletDetails, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) SweepERC20To(ctx context.Context, wallet WalletDetails, contractAddr, toAddress string) (*TransactionResult, error) {
	return nil, errors.New("not implemented")
}

func (b *BaseChain) PrefundGas(ctx context.Context, reserveWallet WalletDetails, userAddress string) (bool, error) {
	return false, errors.New("not implemented")
}

func (f *BaseChain) GenerateMnemonicPhrase() (string, error) {
	if signer.IsProduction() {
		return "", signer.ErrProductionSecretMaterialAccessDisabled
	}
	mnemonic, err := walletcore.GenerateMnemonic(256)
	if err != nil {
		return "", err
	}
	if !walletcore.ValidateMnemonic(mnemonic) {
		return "", errors.New("Invalid Mnemonic")
	}
	return mnemonic, err
}

func (f *BaseChain) GetMnemonic() (string, error) {
	return f.GetMnemonicForPath(context.Background(), "")
}

func (f *BaseChain) GetMnemonicForPath(ctx context.Context, hdPath string) (string, error) {
	if _, err := signer.Authorize(ctx, signer.Request{
		Chain:          f.ChainName,
		ChainID:        int(f.ID),
		KeyReference:   signer.KeyReference(f.ID, hdPath),
		DerivationPath: hdPath,
		Intent:         "wallet.derivation",
		PolicyMetadata: map[string]string{signer.MetadataBoundary: signer.BoundaryWalletDerivation},
	}); err != nil {
		return "", err
	}
	mnemonic := os.Getenv("MNEMONIC_PHRASE")
	if !walletcore.ValidateMnemonic(mnemonic) {
		return "", errors.New("Invalid Mnemonic")
	}
	return mnemonic, nil
}

func (f *BaseChain) GetDerivedPath(purpose, coin, account, change, index int) string {
	return fmt.Sprintf("m/%d'/%d'/%d'/%d/%d", purpose, coin, account, change, index)
}

func (f *BaseChain) GetDerivedPrivateKey(mnemonic string, hdPath string) (string, error) {
	if signer.IsProduction() {
		return "", signer.ErrProductionSecretMaterialAccessDisabled
	}
	return walletcore.DerivePrivateKey(mnemonic, hdPath, f.ID)
}

func (f *BaseChain) CreateHDWalletFromPath(ctx context.Context, hdPath string) (*WalletDetails, error) {
	if signer.IsProduction() {
		return f.GetWatchOnlyWallet(ctx, hdPath)
	}
	mnemonic, err := f.GetMnemonicForPath(ctx, hdPath)
	if err != nil {
		return nil, err
	}
	return f.GetDerivedWallet(mnemonic, hdPath)
}

func (f *BaseChain) GetWatchOnlyWallet(ctx context.Context, hdPath string) (*WalletDetails, error) {
	keyReference := signer.KeyReference(f.ID, hdPath)
	if _, err := signer.Authorize(ctx, signer.Request{
		Chain:          f.ChainName,
		ChainID:        int(f.ID),
		KeyReference:   keyReference,
		DerivationPath: hdPath,
		Intent:         "wallet.watch_only_derivation",
		PolicyMetadata: map[string]string{signer.MetadataBoundary: "external_custody_derivation"},
	}); err != nil {
		return nil, err
	}
	adapter := signer.ActiveCustodyAdapter()
	if adapter == nil {
		return nil, signer.ErrExternalSignerIntegrationRequired
	}
	response, err := adapter.DeriveAddress(ctx, signer.DeriveAddressRequest{
		Chain:          f.ChainName,
		ChainID:        int(f.ID),
		KeyReference:   keyReference,
		DerivationPath: hdPath,
		PolicyMetadata: map[string]string{signer.MetadataBoundary: "external_custody_derivation"},
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(response.Address) == "" {
		return nil, errors.New("external custody adapter returned empty address")
	}
	if strings.TrimSpace(response.KeyReference) != "" {
		keyReference = strings.TrimSpace(response.KeyReference)
	}
	derivationPath := hdPath
	if strings.TrimSpace(response.DerivationPath) != "" {
		derivationPath = strings.TrimSpace(response.DerivationPath)
	}
	signerMode := response.SignerMode
	if strings.TrimSpace(signerMode) == "" {
		signerMode = signer.CurrentMode()
	}
	return &WalletDetails{
		Address:         strings.TrimSpace(response.Address),
		KeyReference:    keyReference,
		DerivationPath:  derivationPath,
		SignerMode:      signerMode,
		CustodyProvider: strings.TrimSpace(response.CustodyProvider),
		WatchOnly:       true,
	}, nil
}

func (f *BaseChain) GetDerivedWallet(mnemonic string, hdPath string) (*WalletDetails, error) {
	if signer.IsProduction() {
		return nil, signer.ErrProductionSecretMaterialAccessDisabled
	}
	wallet, err := walletcore.DeriveWallet(mnemonic, hdPath, f.ID)
	if err != nil {
		return nil, err
	}
	return &WalletDetails{
		Address:        wallet.Address,
		PrivateKey:     wallet.PrivateKey,
		MnemonicPhrase: mnemonic,
		KeyReference:   signer.KeyReference(f.ID, hdPath),
		DerivationPath: hdPath,
		SignerMode:     signer.CurrentMode(),
	}, nil
}

func (b *BaseChain) AddWorker(listener Worker) error {
	if b == nil {
		return ErrNilBaseChain
	}
	if isNilInterface(listener) {
		return ErrNilWorker
	}
	b.Workers = append(b.Workers, listener)
	return nil
}

func (b *BaseChain) RemoveWorker(listener Worker) error {
	for i, l := range b.Workers {
		if l == listener {
			b.Workers = append(b.Workers[:i], b.Workers[i+1:]...)
			return nil
		}
	}
	return errors.New("listener not found")
}

func (b *BaseChain) WorkerCount() int {
	return len(b.Workers)
}

func (b *BaseChain) StartWorkers(ctx context.Context) error {
	if b == nil {
		return ErrNilBaseChain
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for _, listener := range b.Workers {
		if isNilInterface(listener) {
			return ErrNilWorker
		}
	}
	b.ctx, b.cancel = context.WithCancel(ctx)

	for index, listener := range b.Workers {
		var startErr error
		panicErr := helpers.RunSafely(fmt.Sprintf("blockchain.%s.worker.%d.start", b.ChainName, index), func() {
			startErr = listener.Start()
		})
		if panicErr != nil {
			b.cancel()
			return panicErr
		}
		if startErr != nil {
			b.cancel()
			return startErr
		}

		worker := listener
		helpers.GoSafely(fmt.Sprintf("blockchain.%s.worker.%d", b.ChainName, index), func() {
			b.Work(worker)
		})
	}

	return nil
}

func (b *BaseChain) Work(l Worker) {
	if b == nil {
		log.Printf("blockchain worker event loop rejected error=%v", ErrNilBaseChain)
		return
	}
	if b.ctx == nil {
		log.Printf("blockchain worker event loop rejected chain=%q error=%v", b.ChainName, ErrWorkerContextNotInitialized)
		return
	}
	if isNilInterface(l) {
		log.Printf("blockchain worker event loop rejected chain=%q error=%v", b.ChainName, ErrNilWorker)
		return
	}

	for {
		select {

		case <-b.ctx.Done():
			return

		case event, ok := <-l.Events():
			if !ok {
				return
			}

			if verboseEventLoggingEnabled() {
				fmt.Printf("[%s] Event: %v\n", b.ChainName, event)
			}
		}
	}
}

func (b *BaseChain) StopWorkers() error {
	if b.cancel != nil {
		b.cancel()
	}
	for _, listener := range b.Workers {
		if err := listener.Stop(); err != nil {
			return err
		}
	}

	return nil
}
