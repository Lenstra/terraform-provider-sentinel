package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/kevineor/terraform-provider-sentinel/internal/sentinel"
	"github.com/kevineor/terraform-provider-sentinel/internal/sentineltest"
)

// reportTestModel mirrors the object the function returns, so assertions can be
// written against plain Go values.
type reportTestModel struct {
	Passed      bool                    `tfsdk:"passed"`
	ExitCode    int64                   `tfsdk:"exit_code"`
	Total       int64                   `tfsdk:"total"`
	PassedCount int64                   `tfsdk:"passed_count"`
	FailedCount int64                   `tfsdk:"failed_count"`
	Failures    []string                `tfsdk:"failures"`
	Policies    []policyResultTestModel `tfsdk:"policies"`
	Decoded     bool                    `tfsdk:"decoded"`
}

type policyResultTestModel struct {
	Path   string                `tfsdk:"path"`
	Status string                `tfsdk:"status"`
	Errors []string              `tfsdk:"errors"`
	Cases  []caseResultTestModel `tfsdk:"cases"`
}

type caseResultTestModel struct {
	Path         string            `tfsdk:"path"`
	Status       string            `tfsdk:"status"`
	Errors       []string          `tfsdk:"errors"`
	RuleFailures map[string]string `tfsdk:"rule_failures"`
}

func runTestReport(t *testing.T, dir string) reportTestModel {
	t.Helper()

	ctx := context.Background()

	value, funcErr := runFunction(t, NewTestReportFunction(), dir, types.ObjectUnknown(reportAttrTypes))
	if funcErr != nil {
		t.Fatalf("Run: %s", funcErr)
	}

	object, ok := value.(types.Object)
	if !ok {
		t.Fatalf("result is %T, want types.Object", value)
	}

	var model reportTestModel
	if diags := object.As(ctx, &model, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("decoding the report: %s", diags)
	}

	return model
}

func TestTestReportFunctionMetadata(t *testing.T) {
	ctx := context.Background()

	resp := &function.MetadataResponse{}
	NewTestReportFunction().Metadata(ctx, function.MetadataRequest{}, resp)

	if resp.Name != "test_report" {
		t.Errorf("Name = %q, want %q", resp.Name, "test_report")
	}
}

func TestTestReportFunctionDefinition(t *testing.T) {
	ctx := context.Background()

	resp := &function.DefinitionResponse{}
	NewTestReportFunction().Definition(ctx, function.DefinitionRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Definition: %s", resp.Diagnostics)
	}

	ret, ok := resp.Definition.Return.(function.ObjectReturn)
	if !ok {
		t.Fatalf("Return is %T, want function.ObjectReturn", resp.Definition.Return)
	}
	if diff := cmp.Diff(reportAttrTypes, ret.AttributeTypes); diff != "" {
		t.Errorf("attribute types mismatch (-want +got):\n%s", diff)
	}
}

func TestTestReportFunctionRunPassing(t *testing.T) {
	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, sentineltest.PassingReport, "", 0)
	t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

	got := runTestReport(t, dir)

	if !got.Passed {
		t.Errorf("passed = false, want true")
	}
	if got.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", got.ExitCode)
	}
	if !got.Decoded {
		t.Errorf("decoded = false, want true")
	}
	if got.Total != 2 || got.PassedCount != 2 || got.FailedCount != 0 {
		t.Errorf("counts = (%d, %d, %d), want (2, 2, 0)", got.Total, got.PassedCount, got.FailedCount)
	}
	// An empty list rather than null, so configurations can always iterate.
	if diff := cmp.Diff([]string{}, got.Failures); diff != "" {
		t.Errorf("failures mismatch (-want +got):\n%s", diff)
	}

	wantPolicies := []policyResultTestModel{{
		Path:   "restrict-instance-type.sentinel",
		Status: "PASS",
		Errors: []string{},
		Cases: []caseResultTestModel{
			{
				Path:         "test/restrict-instance-type/fail.hcl",
				Status:       "PASS",
				Errors:       []string{},
				RuleFailures: map[string]string{},
			},
			{
				Path:         "test/restrict-instance-type/pass.hcl",
				Status:       "PASS",
				Errors:       []string{},
				RuleFailures: map[string]string{},
			},
		},
	}}
	if diff := cmp.Diff(wantPolicies, got.Policies); diff != "" {
		t.Errorf("policies mismatch (-want +got):\n%s", diff)
	}
}

func TestTestReportFunctionRunFailing(t *testing.T) {
	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, sentineltest.FailingReport, "", 1)
	t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

	got := runTestReport(t, dir)

	if got.Passed {
		t.Errorf("passed = true, want false")
	}
	if got.ExitCode != 1 {
		t.Errorf("exit_code = %d, want 1", got.ExitCode)
	}
	if got.Total != 2 || got.PassedCount != 1 || got.FailedCount != 1 {
		t.Errorf("counts = (%d, %d, %d), want (2, 1, 1)", got.Total, got.PassedCount, got.FailedCount)
	}

	// This is the string a precondition error message shows, so it must stay on
	// one line and name the policy, the case and why the rule failed.
	wantFailures := []string{
		`require-tags.sentinel: test/require-tags/pass.hcl: FAIL: ` +
			`main: expected "main" to be true, got: false`,
	}
	if diff := cmp.Diff(wantFailures, got.Failures); diff != "" {
		t.Errorf("failures mismatch (-want +got):\n%s", diff)
	}

	// The failing case reports why the rule failed. Sentinel puts that in the
	// rule detail and leaves errors empty, so this is the only explanation.
	if len(got.Policies) != 1 {
		t.Fatalf("got %d policies, want 1", len(got.Policies))
	}
	var failing *caseResultTestModel
	for i, testCase := range got.Policies[0].Cases {
		if testCase.Status == "FAIL" {
			failing = &got.Policies[0].Cases[i]
		}
	}
	if failing == nil {
		t.Fatal("no failing case in the report")
	}
	want := map[string]string{"main": `expected "main" to be true, got: false`}
	if diff := cmp.Diff(want, failing.RuleFailures); diff != "" {
		t.Errorf("rule_failures mismatch (-want +got):\n%s", diff)
	}
}

// TestTestReportFunctionRunPolicyError covers a policy that could not be
// processed at all: it has no cases, so the failure is reported against the
// policy and its error message surfaces.
func TestTestReportFunctionRunPolicyError(t *testing.T) {
	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, sentineltest.ErrorReport, "", 1)
	t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

	got := runTestReport(t, dir)

	if got.Passed {
		t.Errorf("passed = true, want false")
	}
	if got.Total != 1 || got.FailedCount != 1 {
		t.Errorf("counts = (%d, %d), want (1, 1)", got.Total, got.FailedCount)
	}

	wantFailures := []string{
		"broken.sentinel: ERROR: broken.sentinel:1:16: expected operand, found 'EOF'",
	}
	if diff := cmp.Diff(wantFailures, got.Failures); diff != "" {
		t.Errorf("failures mismatch (-want +got):\n%s", diff)
	}
}

// TestTestReportFunctionRunPolicyWithoutCases pins that a policy Sentinel
// reports as "?" passes.
func TestTestReportFunctionRunPolicyWithoutCases(t *testing.T) {
	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, sentineltest.SkippedReport, "", 0)
	t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

	got := runTestReport(t, dir)

	if !got.Passed {
		t.Errorf("passed = false, want true")
	}
	if got.Total != 1 || got.PassedCount != 1 || got.FailedCount != 0 {
		t.Errorf("counts = (%d, %d, %d), want (1, 1, 0)", got.Total, got.PassedCount, got.FailedCount)
	}
}

func TestTestReportFunctionRunUnparseableOutput(t *testing.T) {
	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, "FAIL - a.sentinel\n", "something went wrong\n", 1)
	t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

	got := runTestReport(t, dir)

	if got.Passed {
		t.Errorf("passed = true, want false")
	}
	if got.Decoded {
		t.Errorf("decoded = true, want false")
	}
	if len(got.Policies) != 0 {
		t.Errorf("policies = %v, want none", got.Policies)
	}

	// The raw output cannot be returned without breaking the function's
	// reproducibility, so the failure has to say where to look instead.
	if len(got.Failures) != 1 {
		t.Fatalf("got %d failures, want 1: %v", len(got.Failures), got.Failures)
	}
	for _, want := range []string{dir, "sentinel test", "TF_LOG"} {
		if !strings.Contains(got.Failures[0], want) {
			t.Errorf("failure %q does not mention %q", got.Failures[0], want)
		}
	}
}

// TestTestReportFunctionIsReproducible pins the property Terraform relies on: a
// provider defined function is called again during apply, and the run fails if
// it returns anything different from what it returned at plan time. Nothing in
// the result may vary between two runs over the same policies.
func TestTestReportFunctionIsReproducible(t *testing.T) {
	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, sentineltest.FailingReport, "", 1)
	t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

	first := runTestReport(t, dir)
	second := runTestReport(t, dir)

	if diff := cmp.Diff(first, second); diff != "" {
		t.Errorf("the function returned a different result on a second call (-first +second):\n%s", diff)
	}
}

// TestTestReportFunctionAgreesWithTestFunction pins the documented relationship
// between the two functions: test_report's `passed` is what test returns.
func TestTestReportFunctionAgreesWithTestFunction(t *testing.T) {
	tests := map[string]struct {
		report   string
		exitCode int
	}{
		"passing": {report: sentineltest.PassingReport, exitCode: 0},
		"failing": {report: sentineltest.FailingReport, exitCode: 1},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			dir := sentineltest.PolicyDir(t)
			cli := sentineltest.NewCLI(t, test.report, "", test.exitCode)
			t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

			report := runTestReport(t, dir)

			value, funcErr := runFunction(t, NewTestFunction(), dir, types.BoolUnknown())
			if funcErr != nil {
				t.Fatalf("Run: %s", funcErr)
			}
			passed := value.(types.Bool).ValueBool()

			if report.Passed != passed {
				t.Errorf("test_report.passed = %t but test = %t", report.Passed, passed)
			}
		})
	}
}
