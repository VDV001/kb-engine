package cdpbrowser

import (
	"context"
	"time"
)

// withWait bounds ctx by d; нулевой срок — без ограничения сверх ctx.
func withWait(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}
