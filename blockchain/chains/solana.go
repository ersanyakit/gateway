package chains

import (
	"bytes"
	"context"
	blockchain "core/blockchain"
	"core/constants"
	"core/models"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/gagliardetto/solana-go"
	solanaGO "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/rpc"
	"golang.org/x/crypto/pbkdf2"

	solanaSDK "github.com/okx/go-wallet-sdk/coins/solana"
)

const hardened uint32 = 0x80000000

func derive(key []byte, chainCode []byte, segment uint32) ([]byte, []byte) {
	buf := []byte{0}
	buf = append(buf, key...)
	buf = append(buf, big.NewInt(int64(segment)).Bytes()...)
	h := hmac.New(sha512.New, chainCode)
	h.Write(buf)
	I := h.Sum(nil)
	IL := I[:32]
	IR := I[32:]

	return IL, IR
}

type SolanaChain struct {
	blockchain.BaseChain
}

func NewSolanaChain() *SolanaChain {
	return &SolanaChain{

		blockchain.BaseChain{
			ID:          constants.Solana,
			ChainName:   "solana",
			ExplorerURL: "https://explorer.solana.com/",
			RPCHttp:     []string{"https://api.mainnet-beta.solana.com"},
			WebSockets:  []string{"wss://api.mainnet-beta.solana.com"},
		},
	}
}

func (e *SolanaChain) Name() string {
	return e.ChainName
}

func (e *SolanaChain) ChainID() constants.ChainID {
	return e.ID
}

func (s *SolanaChain) NewAddress(privateKeyHex string) (string, error) {

	address, err := solanaSDK.NewAddress(privateKeyHex)

	return address, err
}

func (s *SolanaChain) ValidateAddress(address string) bool {
	if address == "11111111111111111111111111111111" {
		return false
	}

	return solanaSDK.ValidateAddress(address)
}

func (s *SolanaChain) Create(ctx context.Context) (*blockchain.WalletDetails, error) {
	fmt.Printf("[%s]: Creating wallet\n", s.Name())

	mnemonic, err := s.BaseChain.GenerateMnemonicPhrase()
	if err != nil {
		return nil, err
	}

	wallet, err := s.GenerateWalletFromMnemonicSeed(mnemonic, "")
	if err != nil {
		return nil, err
	}

	privateKey := wallet.PrivateKey.String()
	address := wallet.PublicKey().String()

	if !s.ValidateAddress(address) {
		return nil, errors.New("invalid solana address format")
	}

	return &blockchain.WalletDetails{
		Address:        address,
		PrivateKey:     privateKey,
		MnemonicPhrase: mnemonic,
	}, nil
}

func (s *SolanaChain) CreateHDWallet(ctx context.Context, hdAccountId, hdWalletId int) (*blockchain.WalletDetails, error) {
	fmt.Printf("[%s]: Creating HD wallet\n", s.Name())

	mnemonic, err := s.BaseChain.GetMnemonic()
	if err != nil {
		return nil, err
	}

	wallet, err := s.GenerateHDWalletFromMnemonicSeed(mnemonic, "", hdAccountId, hdWalletId)
	if err != nil {
		return nil, err
	}

	privateKey := wallet.PrivateKey.String()
	address := wallet.PublicKey().String()

	if !s.ValidateAddress(address) {
		return nil, errors.New("invalid solana address format")
	}

	return &blockchain.WalletDetails{
		Address:        address,
		PrivateKey:     privateKey,
		MnemonicPhrase: mnemonic,
	}, nil
}

func (s *SolanaChain) GenerateWalletFromMnemonicSeed(mnemonic, password string) (*solana.Wallet, error) {
	pass := []byte("mnemonic")
	if password != "" {
		pass = []byte(password)
	}
	seed := pbkdf2.Key([]byte(mnemonic), pass, 2048, 64, sha512.New)
	h := hmac.New(sha512.New, []byte("ed25519 seed"))
	h.Write(seed)
	sum := h.Sum(nil)

	derivedSeed := sum[:32]
	chain := sum[32:]

	path := []uint32{hardened + uint32(44), hardened + uint32(501), hardened + uint32(0), hardened + uint32(1)}

	for _, segment := range path {
		derivedSeed, chain = derive(derivedSeed, chain, segment)
	}

	key := ed25519.NewKeyFromSeed(derivedSeed)

	wallet, err := solanaGO.WalletFromPrivateKeyBase58(base58.Encode(key))
	if err != nil {
		return nil, err
	}

	return wallet, nil
}

func (s *SolanaChain) GenerateHDWalletFromMnemonicSeed(mnemonic, password string, hdAccountId, hdWalletId int) (*solana.Wallet, error) {
	pass := []byte("mnemonic")
	if password != "" {
		pass = []byte(password)
	}
	seed := pbkdf2.Key([]byte(mnemonic), pass, 2048, 64, sha512.New)
	h := hmac.New(sha512.New, []byte("ed25519 seed"))
	h.Write(seed)
	sum := h.Sum(nil)

	derivedSeed := sum[:32]
	chain := sum[32:]

	path := []uint32{hardened + uint32(44), hardened + uint32(501), hardened + uint32(hdAccountId), hardened + uint32(hdWalletId)}

	for _, segment := range path {
		derivedSeed, chain = derive(derivedSeed, chain, segment)
	}

	key := ed25519.NewKeyFromSeed(derivedSeed)

	wallet, err := solanaGO.WalletFromPrivateKeyBase58(base58.Encode(key))
	if err != nil {
		return nil, err
	}

	return wallet, nil
}

func (s *SolanaChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return s.sendLamports(ctx, wallet, amountRaw, toAddress)
}

func (s *SolanaChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	return s.sendLamports(ctx, wallet, amountRaw, toAddress)
}

func (s *SolanaChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	toAddress, err := solanaSweepDestination()
	if err != nil {
		return nil, err
	}

	rpcClient, err := s.solanaRPCClient()
	if err != nil {
		return nil, err
	}

	privateKey, from, err := solanaPrivateKeyAndAddress(wallet)
	if err != nil {
		return nil, err
	}

	balance, err := rpcClient.GetBalance(ctx, from, rpc.CommitmentFinalized)
	if err != nil {
		return nil, fmt.Errorf("%s balance fetch failed: %w", s.Name(), err)
	}

	const feeLamports uint64 = 5000
	if balance.Value <= feeLamports {
		return nil, fmt.Errorf("%s sweep balance is not enough for fee: balance=%d fee=%d", s.Name(), balance.Value, feeLamports)
	}

	return s.sendLamportsWithClient(ctx, rpcClient, privateKey, from, balance.Value-feeLamports, toAddress)
}

func (s *SolanaChain) sendLamports(ctx context.Context, wallet blockchain.WalletDetails, amountRaw string, toAddress string) (*blockchain.TransactionResult, error) {
	amount, err := nativeAmountRaw(amountRaw)
	if err != nil {
		return nil, err
	}
	if !amount.IsUint64() {
		return nil, errors.New("amount_raw exceeds uint64 lamports")
	}

	rpcClient, err := s.solanaRPCClient()
	if err != nil {
		return nil, err
	}

	privateKey, from, err := solanaPrivateKeyAndAddress(wallet)
	if err != nil {
		return nil, err
	}

	return s.sendLamportsWithClient(ctx, rpcClient, privateKey, from, amount.Uint64(), toAddress)
}

func (s *SolanaChain) sendLamportsWithClient(ctx context.Context, rpcClient *rpc.Client, privateKey solana.PrivateKey, from solana.PublicKey, lamports uint64, toAddress string) (*blockchain.TransactionResult, error) {
	if lamports == 0 {
		return nil, errors.New("amount_raw must be greater than zero")
	}

	to, err := solana.PublicKeyFromBase58(strings.TrimSpace(toAddress))
	if err != nil {
		return nil, fmt.Errorf("invalid solana recipient address: %w", err)
	}

	recent, err := rpcClient.GetLatestBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return nil, fmt.Errorf("%s blockhash fetch failed: %w", s.Name(), err)
	}
	if recent == nil || recent.Value == nil {
		return nil, fmt.Errorf("%s empty latest blockhash", s.Name())
	}

	tx, err := solana.NewTransaction(
		[]solana.Instruction{
			system.NewTransferInstruction(lamports, from, to).Build(),
		},
		recent.Value.Blockhash,
		solana.TransactionPayer(from),
	)
	if err != nil {
		return nil, fmt.Errorf("%s tx build failed: %w", s.Name(), err)
	}

	if _, err := tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(from) {
			return &privateKey
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("%s tx signing failed: %w", s.Name(), err)
	}

	signature, err := rpcClient.SendTransaction(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("%s tx broadcast failed: %w", s.Name(), err)
	}

	return &blockchain.TransactionResult{
		TxHash:  signature.String(),
		Success: true,
	}, nil
}

func (s *SolanaChain) solanaRPCClient() (*rpc.Client, error) {
	for _, rpcURL := range s.RPCs() {
		rpcURL = strings.TrimSpace(rpcURL)
		if rpcURL != "" {
			return rpc.New(rpcURL), nil
		}
	}
	return nil, errors.New("no solana RPC endpoint configured")
}

func solanaPrivateKeyAndAddress(wallet blockchain.WalletDetails) (solana.PrivateKey, solana.PublicKey, error) {
	privateKey, err := solana.PrivateKeyFromBase58(strings.TrimSpace(wallet.PrivateKey))
	if err != nil {
		return nil, solana.PublicKey{}, fmt.Errorf("invalid solana private key: %w", err)
	}

	from := privateKey.PublicKey()
	if wallet.Address != "" && wallet.Address != from.String() {
		return nil, solana.PublicKey{}, fmt.Errorf("private key does not match wallet address: expected %s got %s", wallet.Address, from.String())
	}

	return privateKey, from, nil
}

func solanaSweepDestination() (string, error) {
	for _, key := range []string{"SOLANA_SWEEP_ADDRESS", "SWEEP_ADDRESS"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value, nil
		}
	}
	return "", errors.New("sweep destination is required: set SOLANA_SWEEP_ADDRESS or SWEEP_ADDRESS")
}

func (e *SolanaChain) BatchBalances(ctx context.Context, addresses []string, workers int) []models.BalanceResult {
	jobs := make(chan string, len(addresses))
	results := make(chan models.BalanceResult, len(addresses))

	client := &http.Client{Timeout: 10 * time.Second}

	var wg sync.WaitGroup

	workerFunc := func() {
		defer wg.Done()
		for addr := range jobs {
			balance, err := e.getBalance(client, addr)
			results <- models.BalanceResult{
				Address: addr,
				Balance: balance,
				Error:   err,
			}
		}
	}

	// worker başlat
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go workerFunc()
	}

	// iş kuyruğuna adresleri koy
	for _, addr := range addresses {
		jobs <- addr
	}
	close(jobs)

	wg.Wait()
	close(results)

	var out []models.BalanceResult
	for r := range results {
		out = append(out, r)
	}
	return out
}

func (e *SolanaChain) getBalance(client *http.Client, address string) (string, error) {
	// en iyi RPC seçimi (round-robin basit)
	rpc := e.RPCHttp[0]

	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "getBalance",
		"params":  []interface{}{address, map[string]interface{}{"commitment": "confirmed"}},
	}

	data, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", rpc, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var res struct {
		Result struct {
			Value uint64 `json:"value"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	if res.Error != nil {
		return "", fmt.Errorf("rpc error")
	}

	return fmt.Sprintf("%d", res.Result.Value), nil
}
