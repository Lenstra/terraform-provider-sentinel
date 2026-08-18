package sentinel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/kevineor/terraform-provider-sentinel/internal/sentineltest"
)

func TestRunnerTestPassing(t *testing.T) {
	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, sentineltest.PassingReport, "", 0)

	runner := &Runner{Binary: cli.Binary}
	report, err := runner.Test(context.Background(), dir)
	if err != nil {
		t.Fatalf("Test: %s", err)
	}

	if !report.Passed {
		t.Errorf("Passed = false, want true")
	}
	if report.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", report.ExitCode)
	}
	if report.DurationMs != 42 {
		t.Errorf("DurationMs = %d, want 42", report.DurationMs)
	}
	if len(report.Failures) != 0 {
		t.Errorf("Failures = %v, want none", report.Failures)
	}

	wantPolicies := []PolicyResult{{
		Path:   "restrict-instance-type.sentinel",
		Status: StatusPass,
		Cases: []CaseResult{
			{Path: "test/restrict-instance-type/fail.hcl", Status: StatusPass},
			{Path: "test/restrict-instance-type/pass.hcl", Status: StatusPass},
		},
	}}
	if diff := cmp.Diff(wantPolicies, report.Policies); diff != "" {
		t.Errorf("Policies mismatch (-want +got):\n%s", diff)
	}

	total, passedCount, failed := report.Counts()
	if total != 2 || passedCount != 2 || failed != 0 {
		t.Errorf("Counts() = (%d, %d, %d), want (2, 2, 0)", total, passedCount, failed)
	}

	// The runner must ask for the JSON report and run inside the policy
	// directory, since Sentinel resolves everything relative to sentinel.hcl.
	args, cwd := cli.Invocation(t)
	if args != "test -json" {
		t.Errorf("args = %q, want %q", args, "test -json")
	}
	if want := sentineltest.ResolveDir(t, dir); cwd != want {
		t.Errorf("working directory = %q, want %q", cwd, want)
	}
}

func TestRunnerTestFailing(t *testing.T) {
	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, sentineltest.FailingReport, "", 1)

	runner := &Runner{Binary: cli.Binary}
	report, err := runner.Test(context.Background(), dir)
	if err != nil {
		t.Fatalf("Test returned an error for a failing run, want a report: %s", err)
	}

	if report.Passed {
		t.Errorf("Passed = true, want false")
	}
	if report.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", report.ExitCode)
	}

	wantPolicies := []PolicyResult{{
		Path:   "require-tags.sentinel",
		Status: StatusFail,
		Cases: []CaseResult{
			{Path: "test/require-tags/fail.hcl", Status: StatusPass},
			{
				Path:   "test/require-tags/pass.hcl",
				Status: StatusFail,
				// An assertion failure carries no errors: the reason lives in
				// the rule detail.
				RuleFailures: map[string]string{
					"main": `expected "main" to be true, got: false`,
				},
			},
		},
	}}
	if diff := cmp.Diff(wantPolicies, report.Policies); diff != "" {
		t.Errorf("Policies mismatch (-want +got):\n%s", diff)
	}

	// The failure line has to name the policy, the case, and why the rule failed,
	// since this is what a precondition error message shows.
	wantFailures := []string{
		`require-tags.sentinel: test/require-tags/pass.hcl: FAIL: ` +
			`main: expected "main" to be true, got: false`,
	}
	if diff := cmp.Diff(wantFailures, report.Failures); diff != "" {
		t.Errorf("Failures mismatch (-want +got):\n%s", diff)
	}

	total, passedCount, failed := report.Counts()
	if total != 2 || passedCount != 1 || failed != 1 {
		t.Errorf("Counts() = (%d, %d, %d), want (2, 1, 1)", total, passedCount, failed)
	}
}

// TestRunnerTestPolicyError covers a policy that could not be processed: it has
// no cases, so the failure is reported against the policy itself.
func TestRunnerTestPolicyError(t *testing.T) {
	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, sentineltest.ErrorReport, "", 1)

	runner := &Runner{Binary: cli.Binary}
	report, err := runner.Test(context.Background(), dir)
	if err != nil {
		t.Fatalf("Test: %s", err)
	}

	if report.Passed {
		t.Errorf("Passed = true, want false")
	}

	wantFailures := []string{
		"broken.sentinel: ERROR: broken.sentinel:1:16: expected operand, found 'EOF'",
	}
	if diff := cmp.Diff(wantFailures, report.Failures); diff != "" {
		t.Errorf("Failures mismatch (-want +got):\n%s", diff)
	}

	// A policy with no cases still counts, so a parse failure is visible.
	total, passedCount, failed := report.Counts()
	if total != 1 || passedCount != 0 || failed != 1 {
		t.Errorf("Counts() = (%d, %d, %d), want (1, 0, 1)", total, passedCount, failed)
	}
}

func TestRunnerTestPolicyWithoutCases(t *testing.T) {
	// The "?" status marks a policy with nothing to run, which Sentinel treats
	// as passing.
	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, sentineltest.SkippedReport, "", 0)

	runner := &Runner{Binary: cli.Binary}
	report, err := runner.Test(context.Background(), dir)
	if err != nil {
		t.Fatalf("Test: %s", err)
	}

	if !report.Passed {
		t.Errorf("Passed = false, want true")
	}
	if len(report.Failures) != 0 {
		t.Errorf("Failures = %v, want none", report.Failures)
	}
	total, passedCount, failed := report.Counts()
	if total != 1 || passedCount != 1 || failed != 0 {
		t.Errorf("Counts() = (%d, %d, %d), want (1, 1, 0)", total, passedCount, failed)
	}
}

func TestRunnerTestPoliciesAsArray(t *testing.T) {
	// Sentinel documents the JSON format as subject to change, so the runner
	// accepts an array of objects carrying their own path as well as the
	// documented object keyed by path.
	report := `{
  "duration": 12,
  "policies": [
    {
      "path": "a.sentinel",
      "status": "FAIL",
      "cases": [
        {"path": "test/a/pass.hcl", "status": "FAIL", "errors": ["boom"]}
      ]
    }
  ]
}`

	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, report, "", 1)

	runner := &Runner{Binary: cli.Binary}
	got, err := runner.Test(context.Background(), dir)
	if err != nil {
		t.Fatalf("Test: %s", err)
	}

	want := []PolicyResult{{
		Path:   "a.sentinel",
		Status: StatusFail,
		Cases:  []CaseResult{{Path: "test/a/pass.hcl", Status: StatusFail, Errors: []string{"boom"}}},
	}}
	if diff := cmp.Diff(want, got.Policies); diff != "" {
		t.Errorf("Policies mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"a.sentinel: test/a/pass.hcl: FAIL: boom"}, got.Failures); diff != "" {
		t.Errorf("Failures mismatch (-want +got):\n%s", diff)
	}
}

func TestRunnerTestFailsClosed(t *testing.T) {
	// A zero exit code that disagrees with the report must not let a failing
	// policy through: this gates a Terraform apply.
	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, sentineltest.FailingReport, "", 0)

	runner := &Runner{Binary: cli.Binary}
	got, err := runner.Test(context.Background(), dir)
	if err != nil {
		t.Fatalf("Test: %s", err)
	}

	if got.Passed {
		t.Errorf("Passed = true, want false: a reported failure must override a zero exit code")
	}
}

func TestRunnerTestUnparseableOutput(t *testing.T) {
	// An unusable JSON report degrades to the exit code plus the raw output,
	// rather than failing the run outright. The text here is what the CLI prints
	// without -json.
	tests := map[string]struct {
		exitCode   int
		wantPassed bool
	}{
		"exit zero":     {exitCode: 0, wantPassed: true},
		"exit non zero": {exitCode: 3, wantPassed: false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			dir := sentineltest.PolicyDir(t)
			cli := sentineltest.NewCLI(
				t,
				"PASS - policy.sentinel\n  PASS - test/policy/good.hcl\n",
				"a warning\n",
				test.exitCode,
			)

			runner := &Runner{Binary: cli.Binary}
			report, err := runner.Test(context.Background(), dir)
			if err != nil {
				t.Fatalf("Test: %s", err)
			}

			if report.Passed != test.wantPassed {
				t.Errorf("Passed = %t, want %t", report.Passed, test.wantPassed)
			}
			if report.ExitCode != test.exitCode {
				t.Errorf("ExitCode = %d, want %d", report.ExitCode, test.exitCode)
			}
			if len(report.Policies) != 0 {
				t.Errorf("Policies = %v, want none", report.Policies)
			}
			// Both streams are kept, since neither is a JSON report.
			for _, want := range []string{"PASS - policy.sentinel", "a warning"} {
				if !strings.Contains(report.Output, want) {
					t.Errorf("Output = %q, want it to contain %q", report.Output, want)
				}
			}
		})
	}
}

func TestRunnerTestErrors(t *testing.T) {
	existing := sentineltest.PolicyDir(t)
	emptyDir := t.TempDir()
	file := filepath.Join(existing, ConfigFileName)

	tests := map[string]struct {
		dir     string
		binary  string
		wantErr string
	}{
		"empty directory argument": {
			dir:     "",
			binary:  "sentinel",
			wantErr: "must not be empty",
		},
		"directory does not exist": {
			dir:     filepath.Join(emptyDir, "nope"),
			binary:  "sentinel",
			wantErr: "cannot read the policy directory",
		},
		"path is a file": {
			dir:     file,
			binary:  "sentinel",
			wantErr: "is not a directory",
		},
		"directory has no sentinel.hcl": {
			dir:     emptyDir,
			binary:  "sentinel",
			wantErr: "does not contain a sentinel.hcl file",
		},
		"binary does not exist": {
			dir:     existing,
			binary:  filepath.Join(emptyDir, "no-such-sentinel"),
			wantErr: "failed to run",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			runner := &Runner{Binary: test.binary}
			_, err := runner.Test(context.Background(), test.dir)
			if err == nil {
				t.Fatal("Test succeeded, want an error")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestResolveBinary(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "my-sentinel")
	sentineltest.WriteFile(t, binary, "#!/bin/sh\n")
	if err := os.Chmod(binary, 0o755); err != nil {
		t.Fatalf("chmod: %s", err)
	}

	t.Run("an absolute override is used as is", func(t *testing.T) {
		t.Setenv(BinaryEnvVar, binary)

		got, err := ResolveBinary()
		if err != nil {
			t.Fatalf("ResolveBinary: %s", err)
		}
		if got != binary {
			t.Errorf("got %q, want %q", got, binary)
		}
	})

	t.Run("an override pointing nowhere is an error", func(t *testing.T) {
		t.Setenv(BinaryEnvVar, filepath.Join(dir, "missing", "sentinel"))

		if _, err := ResolveBinary(); err == nil {
			t.Fatal("ResolveBinary succeeded, want an error")
		}
	})

	t.Run("a bare override is looked up in PATH", func(t *testing.T) {
		t.Setenv("PATH", dir)
		t.Setenv(BinaryEnvVar, "my-sentinel")

		got, err := ResolveBinary()
		if err != nil {
			t.Fatalf("ResolveBinary: %s", err)
		}
		if got != binary {
			t.Errorf("got %q, want %q", got, binary)
		}
	})

	t.Run("without an override the default binary is looked up in PATH", func(t *testing.T) {
		t.Setenv("PATH", dir)
		t.Setenv(BinaryEnvVar, "")

		_, err := ResolveBinary()
		if err == nil {
			t.Fatal("ResolveBinary succeeded, want an error")
		}
		// The error must say how to fix the setup.
		for _, want := range []string{DefaultBinary, BinaryEnvVar} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to mention %q", err, want)
			}
		}
	})
}
