package chainresource

import "context"

type databaseReservationContextKey struct{}

// WithDatabaseReservation marks a transfer context whose resource ordering is
// already protected by a durable outbound resource reservation.
func WithDatabaseReservation(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, databaseReservationContextKey{}, true)
}

func HasDatabaseReservation(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(databaseReservationContextKey{}).(bool)
	return value
}
