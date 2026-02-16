package worker

import "context"

type syntheticBrowserMonitorIDContextKey struct{}

func withSyntheticBrowserMonitorID(ctx context.Context, monitorID string) context.Context {
	if monitorID == "" {
		return ctx
	}
	return context.WithValue(ctx, syntheticBrowserMonitorIDContextKey{}, monitorID)
}

func syntheticBrowserMonitorIDFromContext(ctx context.Context) (string, bool) {
	monitorID, ok := ctx.Value(syntheticBrowserMonitorIDContextKey{}).(string)
	return monitorID, ok && monitorID != ""
}
