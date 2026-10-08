//go:build !linux && !darwin

package agent

import (
	"errors"
	"os"
)

func finOpsAllocatedBlocks(os.FileInfo) (string, int64, error) {
	return "", 0, errors.New("allocated block measurement unsupported")
}
