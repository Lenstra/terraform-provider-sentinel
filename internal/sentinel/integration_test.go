package sentinel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests drive the real Sentinel CLI rather than the fake one, so they are
// the only place the assumptions about its interface are actually checked: the
// exit codes, the shape of `sentinel test -json`, and the fact that a failing
// assertion explains itself through rule_detail rather than through errors.
//
// Sentinel is not redistributable, so they skip when the binary is missing.

func realBinary(t *testing.T) string {
	t.Helper()

	binary, err := ResolveBinary()
	if err != nil {
		t.Skipf("the Sentinel CLI is not available: %s", err)
	}
	return binary
}

// examplePolicySet copies the policy set shipped under examples/ into a
// temporary directory, so a test can break a policy without touching the repo.
func examplePolicySet(t *testing.T) string {
	t.Helper()

	source := filepath.Join("..", "..", "examples", "policies")
	dir := filepath.Join(t.TempDir(), "policies")
	if err := os.CopyFS(dir, os.DirFS(source)); err != nil {
		t.Fatalf("copying %s: %s", source, err)
	}
	return dir
}

// TestIntegrationExamplePolicySetPasses runs the example policy set through the
// real CLI. It proves the shipped example is genuinely runnable, and that a
// passing run decodes into a usable report.
func TestIntegrationExamplePolicySetPasses(t *testing.T) {
	runner := &Runner{Binary: realBinary(t)}

	report, err := runner.Test(context.Background(), examplePolicySet(t))
	if err != nil {
		t.Fatalf("Test: %s", err)
	}

	if !report.Passed {
		t.Errorf("Passed = false, want true. Failures: %v\nOutput:\n%s", report.Failures, report.Output)
	}
	if report.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", report.ExitCode)
	}
	if len(report.Failures) != 0 {
		t.Errorf("Failures = %v, want none", report.Failures)
	}

	// The JSON report has to have decoded, otherwise the run degraded to the
	// exit code alone and the format has drifted.
	if len(report.Policies) != 2 {
		t.Fatalf("got %d policies, want 2. Output:\n%s", len(report.Policies), report.Output)
	}
	for _, policy := range report.Policies {
		if policy.Status != StatusPass {
			t.Errorf("policy %q: status = %q, want %s", policy.Path, policy.Status, StatusPass)
		}
		if len(policy.Cases) != 2 {
			t.Errorf("policy %q: got %d cases, want 2", policy.Path, len(policy.Cases))
		}
	}

	total, passedCount, failed := report.Counts()
	if total != 4 || passedCount != 4 || failed != 0 {
		t.Errorf("Counts() = (%d, %d, %d), want (4, 4, 0)", total, passedCount, failed)
	}
}

// TestIntegrationFailingAssertion breaks an assertion and checks the failure is
// reported the way the precondition message relies on.
func TestIntegrationFailingAssertion(t *testing.T) {
	runner := &Runner{Binary: realBinary(t)}
	dir := examplePolicySet(t)

	// The case expects main to be false; asserting the opposite makes it fail.
	testCase := filepath.Join(dir, "test", "require-tags", "fail.hcl")
	content, err := os.ReadFile(testCase)
	if err != nil {
		t.Fatalf("reading %s: %s", testCase, err)
	}
	broken := strings.Replace(string(content), "main = false", "main = true", 1)
	if broken == string(content) {
		t.Fatalf("%s no longer asserts main = false, the fixture needs updating", testCase)
	}
	if err := os.WriteFile(testCase, []byte(broken), 0o644); err != nil {
		t.Fatalf("writing %s: %s", testCase, err)
	}

	report, err := runner.Test(context.Background(), dir)
	if err != nil {
		t.Fatalf("Test returned an error for a failing run, want a report: %s", err)
	}

	if report.Passed {
		t.Errorf("Passed = true, want false")
	}
	if report.ExitCode == 0 {
		t.Errorf("ExitCode = 0, want non zero for a failing run")
	}

	if len(report.Failures) != 1 {
		t.Fatalf("got %d failures, want 1: %v\nOutput:\n%s", len(report.Failures), report.Failures, report.Output)
	}
	// The line has to carry enough to act on: which policy, which case, and why.
	for _, want := range []string{"require-tags.sentinel", "test/require-tags/fail.hcl", StatusFail, "main"} {
		if !strings.Contains(report.Failures[0], want) {
			t.Errorf("failure %q does not mention %q", report.Failures[0], want)
		}
	}

	// Sentinel describes an assertion failure through the rule detail and leaves
	// the case errors empty, so the rule failures are the only explanation.
	var failing *CaseResult
	for _, policy := range report.Policies {
		for i, testCase := range policy.Cases {
			if testCase.Status == StatusFail {
				failing = &policy.Cases[i]
			}
		}
	}
	if failing == nil {
		t.Fatal("no failing case in the report")
	}
	if len(failing.RuleFailures) == 0 {
		t.Errorf("the failing case reports no rule failures, got %+v", failing)
	}
	if _, ok := failing.RuleFailures["main"]; !ok {
		t.Errorf("rule failures do not mention main: %v", failing.RuleFailures)
	}
}

// TestIntegrationPolicyErrorAndNoCases covers the two shapes a policy can take
// besides a normal run: one that fails to parse, and one with no test cases.
func TestIntegrationPolicyErrorAndNoCases(t *testing.T) {
	runner := &Runner{Binary: realBinary(t)}

	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating %s: %s", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %s", path, err)
		}
	}

	write(ConfigFileName, `policy "broken" {
  source = "./broken.sentinel"
}

policy "untested" {
  source = "./untested.sentinel"
}
`)
	// An unterminated rule, so the policy cannot be parsed.
	write("broken.sentinel", "main = rule { \n")
	// Valid, but with no test/untested directory to run.
	write("untested.sentinel", "main = rule { true }\n")

	report, err := runner.Test(context.Background(), dir)
	if err != nil {
		t.Fatalf("Test: %s", err)
	}

	if report.Passed {
		t.Errorf("Passed = true, want false: a policy failed to parse")
	}

	byPath := map[string]PolicyResult{}
	for _, policy := range report.Policies {
		byPath[policy.Path] = policy
	}

	broken, ok := byPath["broken.sentinel"]
	if !ok {
		t.Fatalf("broken.sentinel is missing from the report: %+v", report.Policies)
	}
	if broken.Status != StatusError {
		t.Errorf("broken.sentinel: status = %q, want %s", broken.Status, StatusError)
	}
	if len(broken.Errors) == 0 {
		t.Errorf("broken.sentinel: no errors reported, want the parse error")
	}
	if len(broken.Cases) != 0 {
		t.Errorf("broken.sentinel: got %d cases, want none", len(broken.Cases))
	}

	// A policy with nothing to run is reported as "?" and counts as passing.
	untested, ok := byPath["untested.sentinel"]
	if !ok {
		t.Fatalf("untested.sentinel is missing from the report: %+v", report.Policies)
	}
	if untested.Status != StatusSkip {
		t.Errorf("untested.sentinel: status = %q, want %s", untested.Status, StatusSkip)
	}

	// Only the broken policy failed.
	if len(report.Failures) != 1 {
		t.Errorf("got %d failures, want 1: %v", len(report.Failures), report.Failures)
	}
}
