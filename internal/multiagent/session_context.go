package multiagent

import (
	"context"
	"strings"
)

type sessionRunIDKey struct{}

// WithSessionRunID attaches the current session's run_id to the context so
// tools can enforce "session-bound" behavior (i.e., operate only within the
// active run selected in the UI).
func WithSessionRunID(ctx context.Context, runID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionRunIDKey{}, id)
}

func SessionRunID(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v, ok := ctx.Value(sessionRunIDKey{}).(string)
	if !ok {
		return "", false
	}
	id := strings.TrimSpace(v)
	if id == "" {
		return "", false
	}
	return id, true
}
