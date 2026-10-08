package agent

import "testing"

func TestFinOpsComponentIDExplicitConsistentLabels(t *testing.T) {
	for _, tc := range []struct{ platform, chart, want string }{
		{"backend", "", "backend"}, {"", "mysql", "mysql"},
		{"mysql", "mysql", "mysql"}, {"backend", "mysql", ""}, {"", "", ""}, {"", " mysql", ""},
	} {
		got, err := FinOpsComponentID(map[string]string{"envplane.io/component": tc.platform, "app.kubernetes.io/component": tc.chart})
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Fatalf("%+v got=%q err=%v", tc, got, err)
		}
	}
}
