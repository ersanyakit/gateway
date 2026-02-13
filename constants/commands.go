package constants

import "encoding/json"

// @swagger:model CommandEnvelope
type CommandEnvelope struct {
	// Versiyon numarası
	// example: 1.0
	Version string `json:"version"`

	// Komut tipi
	// enum: merchant.create,merchant.fetch,merchant.domain.create,merchant.domain.fetch,system.create,system.deposit,system.withdraw,system.sweep,system.scan
	// example: merchant.create
	Code CommandType `json:"code"`

	// Payload JSON objesi
	Payload json.RawMessage `json:"payload"`
}

type CommandType string

const (
	CMD_MERCHANT_CREATE        CommandType = "merchant.create"
	CMD_MERCHANT_FETCH         CommandType = "merchant.fetch"
	CMD_MERCHANT_DOMAIN_CREATE CommandType = "merchant.domain.create"
	CMD_MERCHANT_DOMAIN_FETCH  CommandType = "merchant.domain.fetch"
	CMD_MERCHANT_WALLET_CREATE CommandType = "merchant.wallet.create"

	CMD_DEPOSIT  CommandType = "system.deposit"
	CMD_WITHDRAW CommandType = "system.withdraw"
	CMD_SWEEP    CommandType = "system.sweep"
	CMD_SCAN     CommandType = "system.scan"
)

var AllCommands = []CommandType{
	CMD_MERCHANT_CREATE,
	CMD_MERCHANT_FETCH,
	CMD_MERCHANT_DOMAIN_CREATE,
	CMD_MERCHANT_DOMAIN_FETCH,
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
