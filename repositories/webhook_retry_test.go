package repositories

import (
	"errors"
	"testing"
)

type testPermanentError struct {
	err error
}

func (e testPermanentError) Error() string {
	return e.err.Error()
}

func (e testPermanentError) Unwrap() error {
	return e.err
}

func (e testPermanentError) Permanent() bool {
	return true
}

func TestIsPermanentDeliveryError(t *testing.T) {
	if !isPermanentDeliveryError(testPermanentError{err: errors.New("config missing")}) {
		t.Fatal("permanent error should be classified")
	}
	if isPermanentDeliveryError(errors.New("temporary network error")) {
		t.Fatal("plain error should not be permanent")
	}
}
