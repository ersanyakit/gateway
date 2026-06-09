package repositories

import "errors"

type permanentDeliveryError interface {
	Permanent() bool
}

func isPermanentDeliveryError(err error) bool {
	var permanentErr permanentDeliveryError
	return errors.As(err, &permanentErr) && permanentErr.Permanent()
}
