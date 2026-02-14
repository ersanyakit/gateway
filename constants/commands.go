package constants

import "encoding/json"

type CommandEnvelope struct {
	Version string          `json:"version"`
	Code    CommandType     `json:"code"`
	Payload json.RawMessage `json:"payload"`
}

type CommandType string

const (
	CMD_MERCHANT_CREATE          CommandType = "merchant.create"
	CMD_MERCHANT_FETCH           CommandType = "merchant.fetch"
	CMD_MERCHANT_FETCH_BY_ID     CommandType = "merchant.fetch.by_id"
	CMD_MERCHANT_FETCH_BY_EMAIL  CommandType = "merchant.fetch.by_email"
	CMD_MERCHANT_DELETE_BY_ID    CommandType = "merchant.delete.by_id"
	CMD_MERCHANT_DELETE_BY_EMAIL CommandType = "merchant.delete.by_email"
	CMD_MERCHANT_DOMAIN_CREATE   CommandType = "merchant.domain.create"
	CMD_MERCHANT_DOMAIN_FETCH    CommandType = "merchant.domain.fetch"
	CMD_MERCHANT_WALLET_CREATE   CommandType = "merchant.wallet.create"
	CMD_DEPOSIT                  CommandType = "system.deposit"
	CMD_WITHDRAW                 CommandType = "system.withdraw"
	CMD_SWEEP                    CommandType = "system.sweep"
	CMD_SCAN                     CommandType = "system.scan"
)

var AllCommands = []CommandType{
	CMD_MERCHANT_CREATE,
	CMD_MERCHANT_FETCH,
	CMD_MERCHANT_FETCH_BY_ID,
	CMD_MERCHANT_FETCH_BY_EMAIL,
	CMD_MERCHANT_DOMAIN_CREATE,
	CMD_MERCHANT_DOMAIN_FETCH,
	CMD_MERCHANT_WALLET_CREATE,
	CMD_DEPOSIT,
	CMD_WITHDRAW,
	CMD_SWEEP,
	CMD_SCAN,
}

func (c CommandType) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(c))
}

func (c *CommandType) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*c = CommandType(s)
	return nil
}

func (c CommandType) String() string {
	return string(c)
}
