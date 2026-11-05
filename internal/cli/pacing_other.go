//go:build !linux && !darwin

package cli

import (
	"context"
	"fmt"
	"os"
)

func lockConnection(context.Context, *os.File) error {
	return fmt.Errorf("cross-process connection pacing is supported on Linux and macOS")
}
