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
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/gagliardetto/solana-go"
	solanaGO "github.com/gagliardetto/solana-go"
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

func (s *SolanaChain) Deposit(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Depositing %f to %s\n", s.Name(), amount, toAddress)
	return &blockchain.TransactionResult{TxHash: "DepositTxHash", Success: true}, nil
}

func (s *SolanaChain) Withdraw(ctx context.Context, wallet blockchain.WalletDetails, amount float64, toAddress string) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Withdrawing %f from %s\n", s.Name(), amount, wallet.Address)
	return &blockchain.TransactionResult{TxHash: "WithdrawTxHash", Success: true}, nil
}

func (s *SolanaChain) Sweep(ctx context.Context, wallet blockchain.WalletDetails) (*blockchain.TransactionResult, error) {
	fmt.Printf("[%s]: Sweeping wallet", s.Name())
	return &blockchain.TransactionResult{TxHash: "SweepTxHash", Success: true}, nil
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
		"method":  "eth_getBalance",
		"params":  []interface{}{address, "latest"},
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
		Result string          `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	if res.Error != nil {
		return "", fmt.Errorf("rpc error")
	}

	return res.Result, nil
}
