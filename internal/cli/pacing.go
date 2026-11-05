package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const connectionInterval = 500 * time.Millisecond

// waitForConnection is lazy: help, dry runs and policy refusals need no cache.
// One namespace covers both environments and REST/stream hosts, without storing
// accounts, tokens or URLs. The SDK invokes it only for actual new connections.
func waitForConnection(ctx context.Context) error {
	dir, err := os.UserCacheDir()
	if err != nil {
		return fmt.Errorf("connection pacing needs a user cache directory: %w", err)
	}
	return paceConnection(ctx, filepath.Join(dir, "oandactl"))
}

func paceConnection(ctx context.Context, dir string) error {
	// Bound lock acquisition even if the transport's dial context has no deadline.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("connection pacing cache: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "connections"), os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- per-user cache state
	if err != nil {
		return fmt.Errorf("connection pacing state: %w", err)
	}
	// Closing releases the OS lock, including on process exit; never unlink a
	// locked file, which would let other processes lock a different inode.
	defer func() { _ = f.Close() }()
	if err := lockConnection(ctx, f); err != nil {
		return fmt.Errorf("connection pacing lock: %w", err)
	}
	b, err := io.ReadAll(io.LimitReader(f, 65))
	if err != nil {
		return fmt.Errorf("connection pacing read: %w", err)
	}
	if len(b) > 0 {
		n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err != nil || len(b) > 64 || n <= 0 {
			return fmt.Errorf("invalid connection pacing timestamp")
		}
		now := time.Now()
		last := time.Unix(0, n)
		if last.After(now) {
			return fmt.Errorf("connection pacing timestamp is in the future; check the system clock")
		}
		if delay := connectionInterval - now.Sub(last); delay > 0 {
			if err := waitConnectionDelay(ctx, delay); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("connection pacing truncate: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("connection pacing seek: %w", err)
	}
	if _, err := f.WriteString(strconv.FormatInt(time.Now().UnixNano(), 10) + "\n"); err != nil {
		return fmt.Errorf("connection pacing write: %w", err)
	}
	return nil
}

func waitConnectionDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
