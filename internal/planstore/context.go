package planstore

import "context"

type storeKey struct{}

// WithStore stamps ctx with the plan store so the built-in plan tools can reach
// it from inside the run loop. The driver injects the workspace store; outside
// strict plan mode the tools fail closed.
func WithStore(ctx context.Context, s *Store) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, storeKey{}, s)
}

// StoreFromContext returns the active plan store, if any.
func StoreFromContext(ctx context.Context) (*Store, bool) {
	if ctx == nil {
		return nil, false
	}
	s, ok := ctx.Value(storeKey{}).(*Store)
	return s, ok && s != nil
}
