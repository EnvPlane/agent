package agent

import (
	"errors"
	"testing"
)

func TestPVCUsageFailureCategoryNeverExposesRawErrors(t *testing.T) {
	if got := pvcUsageFailureCategory(errors.New("private path /data/customer; token=secret")); got != "measurement_unavailable" {
		t.Fatalf("raw error escaped: %q", got)
	}
	if got := pvcUsageFailureCategory(errors.New("PVC symlink prevents complete confined measurement")); got != "symlink_unsupported" {
		t.Fatal(got)
	}
}
