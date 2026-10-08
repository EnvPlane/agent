//go:build linux || darwin

package agent

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func finOpsAllocatedBlocks(info os.FileInfo) (string, int64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Blocks < 0 || stat.Blocks > (1<<63-1)/512 {
		return "", 0, errors.New("allocated block metadata unavailable")
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), stat.Blocks * 512, nil
}
