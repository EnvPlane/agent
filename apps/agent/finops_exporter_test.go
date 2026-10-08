package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedPVCRefsFileBounds(t *testing.T) {
	valid := `[{"Namespace":"approved","PVCName":"data","PVCUID":"2f520e00-52f4-412f-be97-a4570ce45e97","ComponentID":"backend"}]`
	for _, tc := range []struct {
		name, payload string
		valid         bool
	}{
		{"valid", valid, true}, {"empty", `[]`, false},
		{"unknown", `[{"HostPath":"/"}]`, false}, {"trailing", valid + `{}`, false},
		{"oversized", valid + strings.Repeat(" ", 16384), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "refs.json")
			if err := os.WriteFile(path, []byte(tc.payload), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := readPinnedPVCRefs(path)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
