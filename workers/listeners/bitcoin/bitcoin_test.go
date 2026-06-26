package bitcoin

import (
	"errors"
	"net/http"
	"testing"
)

func TestIsBitcoinTxPageEOF(t *testing.T) {
	err := &bitcoinAPIError{statusCode: http.StatusNotFound, body: "start index out of range"}
	if !isBitcoinTxPageEOF(err) {
		t.Fatal("start-index 404 should be treated as end of block tx pages")
	}
}

func TestIsBitcoinTxPageEOFRejectsOtherErrors(t *testing.T) {
	cases := []error{
		&bitcoinAPIError{statusCode: http.StatusNotFound, body: "block not found"},
		&bitcoinAPIError{statusCode: http.StatusInternalServerError, body: "start index out of range"},
		errors.New("start index out of range"),
	}

	for _, err := range cases {
		if isBitcoinTxPageEOF(err) {
			t.Fatalf("isBitcoinTxPageEOF(%v) = true, want false", err)
		}
	}
}
