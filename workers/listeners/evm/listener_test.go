package evm

import (
	"errors"
	"testing"
)

func TestIsTraceUnavailableError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "method missing", err: errors.New("chiliz RPC trace_block error -32601: the method trace_block does not exist/is not available"), want: true},
		{name: "method not allowed", err: errors.New("avalanche RPC trace_block error -32600: Method trace_block not allowed"), want: true},
		{name: "provider tier", err: errors.New("arbitrum returned HTTP 400: method is not available on freetier"), want: true},
		{name: "transient", err: errors.New("context deadline exceeded"), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTraceUnavailableError(tc.err); got != tc.want {
				t.Fatalf("isTraceUnavailableError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsBlockReceiptsUnavailableError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "method missing", err: errors.New("ethereum RPC eth_getBlockReceipts error -32601: method eth_getBlockReceipts does not exist"), want: true},
		{name: "method unavailable", err: errors.New("ethereum RPC eth_getBlockReceipts error -32600: method is not available"), want: true},
		{name: "different method", err: errors.New("ethereum RPC trace_block error -32601: method trace_block does not exist"), want: false},
		{name: "transient", err: errors.New("context deadline exceeded"), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBlockReceiptsUnavailableError(tc.err); got != tc.want {
				t.Fatalf("isBlockReceiptsUnavailableError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
