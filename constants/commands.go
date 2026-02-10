package constants

import "encoding/json"

type CommandEnvelope struct {
	Version string          `json:"version"`
	Code    string          `json:"code"`
	Payload json.RawMessage `json:"payload"`
}

type TCommandTypes int

const (
	CMD_CREATE_WALLET = "system.create"
	CMD_DEPOSIT       = "system.deposit"
	CMD_WITHDRAW      = "system.withdraw"
	CMD_SWEEP         = "system.sweep"
	CMD_SCAN          = "system.scan"
)
