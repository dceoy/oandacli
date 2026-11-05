//go:build linux || darwin

package cli

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

func lockConnection(ctx context.Context, f *os.File) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return err
		}
		if err := waitConnectionDelay(ctx, 10*time.Millisecond); err != nil {
			return err
		}
	}
}
