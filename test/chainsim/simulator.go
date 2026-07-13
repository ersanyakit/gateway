package chainsim

import (
	"fmt"
	"strings"

	"core/constants"
	"core/types"
)

type Block struct {
	Height     int64
	Hash       string
	ParentHash string
	Canonical  bool
}

type Transfer struct {
	ChainID   constants.ChainID
	ChainName string
	Block     Block
	TxHash    string
	LogIndex  string
	From      string
	To        string
	AmountRaw string
	Symbol    string
	Token     string
	Decimals  uint8
	EventType string
}

type Simulator struct {
	chainID constants.ChainID
	blocks  []Block
	events  []Transfer
}

func New(chainID constants.ChainID) *Simulator {
	return &Simulator{chainID: chainID}
}

func (s *Simulator) EmitBlock(hash string) Block {
	height := int64(len(s.blocks) + 1)
	parent := ""
	if len(s.blocks) > 0 {
		parent = s.blocks[len(s.blocks)-1].Hash
	}
	block := Block{
		Height:     height,
		Hash:       strings.TrimSpace(hash),
		ParentHash: parent,
		Canonical:  true,
	}
	if block.Hash == "" {
		block.Hash = fmt.Sprintf("%s-%d", constants.ChainName(s.chainID), height)
	}
	s.blocks = append(s.blocks, block)
	return block
}

func (s *Simulator) EmitNativeTransfer(block Block, txHash, logIndex, from, to, amountRaw, symbol string) Transfer {
	return s.emitTransfer(block, Transfer{
		TxHash:    txHash,
		LogIndex:  logIndex,
		From:      from,
		To:        to,
		AmountRaw: amountRaw,
		Symbol:    symbol,
		EventType: "native_transfer",
	})
}

func (s *Simulator) EmitTokenTransfer(block Block, txHash, logIndex, from, to, amountRaw, symbol, token string, decimals uint8) Transfer {
	return s.emitTransfer(block, Transfer{
		TxHash:    txHash,
		LogIndex:  logIndex,
		From:      from,
		To:        to,
		AmountRaw: amountRaw,
		Symbol:    symbol,
		Token:     token,
		Decimals:  decimals,
		EventType: "token_transfer",
	})
}

func (s *Simulator) Reorg(depth int, replacementHashes ...string) []Block {
	if depth <= 0 || len(s.blocks) == 0 {
		return nil
	}
	if depth > len(s.blocks) {
		depth = len(s.blocks)
	}
	start := len(s.blocks) - depth
	reorged := append([]Block(nil), s.blocks[start:]...)
	for i := start; i < len(s.blocks); i++ {
		s.blocks[i].Canonical = false
	}
	for i := range reorged {
		reorged[i].Canonical = false
	}
	s.events = filterCanonicalTransfers(s.events, s.blocks[:start])
	s.blocks = s.blocks[:start]
	for _, hash := range replacementHashes {
		s.EmitBlock(hash)
	}
	return reorged
}

func (s *Simulator) Transfers() []Transfer {
	out := make([]Transfer, len(s.events))
	copy(out, s.events)
	return out
}

func (s *Simulator) TransactionParams() []types.TransactionParam {
	out := make([]types.TransactionParam, 0, len(s.events))
	for _, event := range s.events {
		out = append(out, event.TransactionParam())
	}
	return out
}

func (t Transfer) TransactionParam() types.TransactionParam {
	block := fmt.Sprintf("%d", t.Block.Height)
	txHash := strings.TrimSpace(t.TxHash)
	logIndex := strings.TrimSpace(t.LogIndex)
	symbol := strings.TrimSpace(t.Symbol)
	amount := strings.TrimSpace(t.AmountRaw)
	from := strings.TrimSpace(t.From)
	to := strings.TrimSpace(t.To)
	blockHash := strings.TrimSpace(t.Block.Hash)
	parentHash := strings.TrimSpace(t.Block.ParentHash)
	status := "confirmed"
	var token *string
	if strings.TrimSpace(t.Token) != "" {
		value := strings.TrimSpace(t.Token)
		token = &value
	}
	return types.TransactionParam{
		ChainID:    t.ChainID,
		Hash:       &txHash,
		Block:      &block,
		BlockHash:  &blockHash,
		ParentHash: &parentHash,
		Token:      token,
		Symbol:     &symbol,
		Decimals:   t.Decimals,
		From:       &from,
		To:         &to,
		Amount:     &amount,
		LogIndex:   &logIndex,
		Status:     &status,
	}
}

func (s *Simulator) emitTransfer(block Block, event Transfer) Transfer {
	event.ChainID = s.chainID
	event.ChainName = constants.ChainName(s.chainID)
	event.Block = block
	if event.Symbol == "" {
		event.Symbol = strings.ToUpper(event.ChainName)
	}
	if event.Decimals == 0 {
		event.Decimals = defaultDecimals(s.chainID)
	}
	if event.LogIndex == "" {
		event.LogIndex = fmt.Sprintf("log:%d", len(s.events))
	}
	s.events = append(s.events, event)
	return event
}

func filterCanonicalTransfers(events []Transfer, blocks []Block) []Transfer {
	canonical := make(map[string]struct{}, len(blocks))
	for _, block := range blocks {
		canonical[block.Hash] = struct{}{}
	}
	out := events[:0]
	for _, event := range events {
		if _, ok := canonical[event.Block.Hash]; ok {
			out = append(out, event)
		}
	}
	return out
}

func defaultDecimals(chainID constants.ChainID) uint8 {
	switch chainID {
	case constants.Bitcoin:
		return 8
	case constants.Solana:
		return 9
	case constants.TRON, constants.TRONTestnet:
		return 6
	default:
		return 18
	}
}
