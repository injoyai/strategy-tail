//go:build !windows

package main

import "context"

func runDesktop(ctx context.Context, _ context.CancelFunc, cfg config, server string, cursor int64) {
	run(ctx, cfg, server, cursor)
}
