package chains

import "testing"

func TestEthereumNewAddressFromPrivateKey(t *testing.T) {
	chain := NewEthereumChain()
	address, err := chain.NewAddress("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if address != "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf" {
		t.Fatalf("ethereum address = %q", address)
	}
	if !chain.ValidateAddress(address) {
		t.Fatalf("generated ethereum address did not validate: %s", address)
	}
}

func TestBitcoinAddressValidation(t *testing.T) {
	chain := NewBitcoinChain()
	address, err := chain.NewAddress("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if !chain.ValidateAddress(address) {
		t.Fatalf("generated bitcoin address rejected: %s", address)
	}
	if chain.ValidateAddress("0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf") {
		t.Fatal("ethereum address should not validate as bitcoin")
	}
}

func TestTronAddressValidation(t *testing.T) {
	chain := NewTronChain()
	valid := "TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7"
	if !chain.ValidateAddress(valid) {
		t.Fatalf("valid tron address rejected: %s", valid)
	}
	if chain.ValidateAddress("So11111111111111111111111111111111111111112") {
		t.Fatal("solana address should not validate as tron")
	}
}

func TestSolanaAddressValidation(t *testing.T) {
	chain := NewSolanaChain()
	valid := "So11111111111111111111111111111111111111112"
	if !chain.ValidateAddress(valid) {
		t.Fatalf("valid solana address rejected: %s", valid)
	}
	if chain.ValidateAddress("11111111111111111111111111111111") {
		t.Fatal("system program id should not be accepted as a deposit wallet")
	}
	if chain.ValidateAddress("0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf") {
		t.Fatal("ethereum address should not validate as solana")
	}
}
