package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PinnedPVCUsageRef is operator-reviewed metadata, not Agent/UI autodiscovery.
// Each approved claim must be mounted read-only at <base>/<immutable PVC UID>.
type PinnedPVCUsageRef struct{ Namespace, PVCName, PVCUID, ComponentID string }
type PVCUsageReading struct {
	Ref            PinnedPVCUsageRef
	AllocatedBytes int64
	ObservedAt     time.Time
}
type PinnedPVCUsageSampler struct {
	base   string
	refs   []PinnedPVCUsageRef
	verify func(context.Context, PinnedPVCUsageRef) error
}

func NewPinnedPVCUsageSampler(base string, refs []PinnedPVCUsageRef, verify func(context.Context, PinnedPVCUsageRef) error) (*PinnedPVCUsageSampler, error) {
	if !filepath.IsAbs(base) || len(refs) == 0 || len(refs) > 5 || verify == nil {
		return nil, errors.New("approved bounded PVC sampler configuration required")
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.Namespace == "" || ref.PVCName == "" || ref.ComponentID == "" || !finOpsKubernetesUID.MatchString(ref.PVCUID) || seen[ref.PVCUID] {
			return nil, errors.New("invalid or duplicate pinned PVC scope")
		}
		seen[ref.PVCUID] = true
	}
	return &PinnedPVCUsageSampler{base: base, refs: append([]PinnedPVCUsageRef(nil), refs...), verify: verify}, nil
}

// Sample reads inode/block metadata only. It never executes commands, reads
// file contents, follows dataset symlinks, or uses node-wide filesystem stats.
// Verification before and after scanning detects changed PVC generations.
func (s *PinnedPVCUsageSampler) Sample(ctx context.Context) ([]PVCUsageReading, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result := make([]PVCUsageReading, 0, len(s.refs))
	for _, ref := range s.refs {
		if err := s.verify(ctx, ref); err != nil {
			return nil, errors.New("pinned PVC identity unavailable")
		}
		path := filepath.Join(s.base, ref.PVCUID)
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("reviewed read-only PVC mount unavailable")
		}
		root, err := os.OpenRoot(path)
		if err != nil {
			return nil, errors.New("PVC mount open unavailable")
		}
		used := int64(0)
		entries := 0
		seen := map[string]bool{}
		err = fs.WalkDir(root.FS(), ".", func(_ string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return errors.New("PVC metadata scan unavailable")
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			entries++
			if entries > 100000 {
				return errors.New("PVC metadata scan bound exceeded")
			}
			// WalkDir never follows symlinks; DirEntry.Info describes the link
			// inode itself. Count its allocation, not any referenced dataset.
			info, e := entry.Info()
			if e != nil {
				return errors.New("PVC inode metadata unavailable")
			}
			if !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return nil
			}
			identity, bytes, e := finOpsAllocatedBlocks(info)
			if e != nil {
				return e
			}
			if seen[identity] {
				return nil
			}
			seen[identity] = true
			if bytes < 0 || used > (1<<63-1)-bytes {
				return errors.New("PVC byte count overflow")
			}
			used += bytes
			return nil
		})
		_ = root.Close()
		if err != nil {
			return nil, err
		}
		if err = s.verify(ctx, ref); err != nil {
			return nil, errors.New("pinned PVC generation changed")
		}
		result = append(result, PVCUsageReading{Ref: ref, AllocatedBytes: used, ObservedAt: time.Now().UTC()})
	}
	return result, nil
}

// PrometheusText contains only pinned identifiers and bytes, never file names
// or contents. Its HTTP/TLS serving and deployment require operator review.
func PVCUsagePrometheusText(readings []PVCUsageReading) string {
	var output strings.Builder
	output.WriteString("# TYPE envplane_pvc_directory_allocated_bytes gauge\n")
	for _, reading := range readings {
		fmt.Fprintf(&output, "envplane_pvc_directory_allocated_bytes{namespace=%q,persistentvolumeclaim=%q,pvc_uid=%q,component=%q} %d\n", reading.Ref.Namespace, reading.Ref.PVCName, reading.Ref.PVCUID, reading.Ref.ComponentID, reading.AllocatedBytes)
	}
	return output.String()
}
