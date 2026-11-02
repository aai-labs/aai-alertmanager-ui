package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Execute the real expression against differently named self-scrape targets,
// a failed target and missing self-monitoring. Optional locally installed tool.
func TestCoverageWithArbitraryPrometheusJobNames(t *testing.T) {
	tool, err := exec.LookPath("promtool")
	if err != nil {
		t.Skip("promtool not installed")
	}
	fixture := fmt.Sprintf(`rule_files: []
evaluation_interval: 1m
tests:
- interval: 1m
  input_series:
  - series: 'up{job="company-observer",instance="prometheus-a"}'
    values: '1 1 0 1 1'
  - series: 'prometheus_build_info{job="company-observer",instance="prometheus-a",version="custom"}'
    values: '1 1 1 stale'
  - series: 'up{job="unrelated-service",instance="app"}'
    values: '1 1 1 1 1'
  promql_expr_test:
  - expr: %q
    eval_time: 1m
    exp_samples:
    - labels: '{}'
      value: 1
  - expr: %q
    eval_time: 2m
    exp_samples:
    - labels: '{}'
      value: 0
  - expr: %q
    eval_time: 4m
    exp_samples: []
`, "max("+analyticsDefaultCoverage+")", "max("+analyticsDefaultCoverage+")", "max("+analyticsDefaultCoverage+")")
	path := filepath.Join(t.TempDir(), "coverage.yml")
	if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(tool, "test", "rules", path).CombinedOutput(); err != nil {
		t.Fatalf("coverage semantics: %v\n%s", err, output)
	}
}
