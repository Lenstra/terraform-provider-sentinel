package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/lenstra/terraform-provider-sentinel/internal/sentinel"
)

var _ function.Function = &TestReportFunction{}

// TestReportFunction runs `sentinel test` and returns the detail of the run, so
// that a precondition can explain what failed instead of only refusing to apply.
type TestReportFunction struct{}

func NewTestReportFunction() function.Function {
	return &TestReportFunction{}
}

// caseResultType describes a single test case of a policy.
var caseResultType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"path":          types.StringType,
		"status":        types.StringType,
		"errors":        types.ListType{ElemType: types.StringType},
		"rule_failures": types.MapType{ElemType: types.StringType},
	},
}

// policyResultType describes the results of one policy.
var policyResultType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"path":   types.StringType,
		"status": types.StringType,
		"errors": types.ListType{ElemType: types.StringType},
		"cases":  types.ListType{ElemType: caseResultType},
	},
}

// reportAttrTypes describes the object the function returns.
//
// Every field has to be reproducible: Terraform calls a provider defined
// function again during apply and fails the run if the result differs from the
// one it got at plan time. That rules out the run time the CLI reports and its
// raw output, which embeds timings, so neither is returned. The raw output is
// written to the provider log instead, where TF_LOG can pick it up.
var reportAttrTypes = map[string]attr.Type{
	"passed":       types.BoolType,
	"exit_code":    types.Int64Type,
	"total":        types.Int64Type,
	"passed_count": types.Int64Type,
	"failed_count": types.Int64Type,
	"failures":     types.ListType{ElemType: types.StringType},
	"policies":     types.ListType{ElemType: policyResultType},
	"decoded":      types.BoolType,
}

func (f *TestReportFunction) Metadata(ctx context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "test_report"
}

func (f *TestReportFunction) Definition(ctx context.Context, req function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Run the Sentinel tests of a policy set and return the detailed results",
		Description: "Runs `sentinel test` in the given directory and returns an object describing the run: " +
			"`passed`, `exit_code`, `total`, `passed_count`, `failed_count`, `failures` (one line per " +
			"failure), `policies` (per policy results) and `decoded` (whether the JSON report could be " +
			"read).\n\n" +
			"Use this instead of `provider::sentinel::test` when the precondition should say *what* failed. " +
			"`passed` carries the same value `provider::sentinel::test` returns.\n\n" +
			"`passed` and `exit_code` come from the CLI exit status. The remaining fields come from " +
			"`sentinel test -json`, whose format Sentinel documents as subject to change: if it cannot be " +
			"decoded, `decoded` is `false`, `policies` is empty and `failures` explains where to look.\n\n" +
			"The result has to be reproducible, since Terraform calls the function again during apply and " +
			"fails the run if it returns something different. That is why neither the run time nor the raw " +
			"CLI output is returned; the raw output goes to the provider log, visible with `TF_LOG=debug`.",
		Parameters: []function.Parameter{
			function.StringParameter{
				Name:        "dir",
				Description: "Directory holding the `sentinel.hcl` file and the tests to run.",
			},
		},
		Return: function.ObjectReturn{
			AttributeTypes: reportAttrTypes,
		},
	}
}

func (f *TestReportFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var dir string
	if resp.Error = req.Arguments.Get(ctx, &dir); resp.Error != nil {
		return
	}

	runner := &sentinel.Runner{}
	report, err := runner.Test(ctx, dir)
	if err != nil {
		resp.Error = function.ConcatFuncErrors(resp.Error, function.NewFuncError(err.Error()))
		return
	}

	// The raw output cannot be returned without breaking the function's
	// reproducibility, so this is the only place it survives.
	tflog.Debug(ctx, "ran sentinel test", map[string]any{
		"dir":       dir,
		"exit_code": report.ExitCode,
		"output":    report.Output,
	})

	value, diags := reportValue(ctx, dir, report)
	if diags.HasError() {
		resp.Error = function.ConcatFuncErrors(resp.Error, function.FuncErrorFromDiags(ctx, diags))
		return
	}

	resp.Error = function.ConcatFuncErrors(resp.Error, resp.Result.Set(ctx, value))
}

// reportValue converts a report into the object the function returns.
func reportValue(ctx context.Context, dir string, report *sentinel.Report) (types.Object, diag.Diagnostics) {
	total, passed, failed := report.Counts()

	// A run that failed without a readable report would otherwise come back with
	// nothing to act on, since the raw output cannot be returned. Point at where
	// to look instead, in terms that do not change between plan and apply.
	decoded := report.Decoded
	failureList := report.Failures
	if !decoded && !report.Passed {
		failureList = append(failureList, fmt.Sprintf(
			"the Sentinel CLI exited %d in %q without a readable JSON report; "+
				"run `sentinel test` there, or set TF_LOG=debug to see its output",
			report.ExitCode, dir,
		))
	}

	failures, diags := stringList(ctx, failureList)
	if diags.HasError() {
		return types.ObjectNull(reportAttrTypes), diags
	}

	policies := make([]attr.Value, 0, len(report.Policies))
	for _, policy := range report.Policies {
		cases := make([]attr.Value, 0, len(policy.Cases))
		for _, testCase := range policy.Cases {
			caseErrors, caseDiags := stringList(ctx, testCase.Errors)
			diags.Append(caseDiags...)

			ruleFailures, ruleDiags := stringMap(ctx, testCase.RuleFailures)
			diags.Append(ruleDiags...)

			value, valueDiags := types.ObjectValue(caseResultType.AttrTypes, map[string]attr.Value{
				"path":          types.StringValue(testCase.Path),
				"status":        types.StringValue(testCase.Status),
				"errors":        caseErrors,
				"rule_failures": ruleFailures,
			})
			diags.Append(valueDiags...)
			cases = append(cases, value)
		}

		caseList, caseListDiags := types.ListValue(caseResultType, cases)
		diags.Append(caseListDiags...)

		policyErrors, policyErrorDiags := stringList(ctx, policy.Errors)
		diags.Append(policyErrorDiags...)

		value, policyDiags := types.ObjectValue(policyResultType.AttrTypes, map[string]attr.Value{
			"path":   types.StringValue(policy.Path),
			"status": types.StringValue(policy.Status),
			"errors": policyErrors,
			"cases":  caseList,
		})
		diags.Append(policyDiags...)
		policies = append(policies, value)
	}

	policyList, policyListDiags := types.ListValue(policyResultType, policies)
	diags.Append(policyListDiags...)

	if diags.HasError() {
		return types.ObjectNull(reportAttrTypes), diags
	}

	value, objectDiags := types.ObjectValue(reportAttrTypes, map[string]attr.Value{
		"passed":       types.BoolValue(report.Passed),
		"exit_code":    types.Int64Value(int64(report.ExitCode)),
		"total":        types.Int64Value(int64(total)),
		"passed_count": types.Int64Value(int64(passed)),
		"failed_count": types.Int64Value(int64(failed)),
		"failures":     failures,
		"policies":     policyList,
		"decoded":      types.BoolValue(decoded),
	})
	diags.Append(objectDiags...)

	return value, diags
}

// stringList converts a slice into a list of strings, mapping nil to an empty
// list rather than a null one so callers can always iterate over the result.
func stringList(ctx context.Context, values []string) (types.List, diag.Diagnostics) {
	if values == nil {
		values = []string{}
	}
	return types.ListValueFrom(ctx, types.StringType, values)
}

// stringMap does the same for a map, so an absent one reads as empty.
func stringMap(ctx context.Context, values map[string]string) (types.Map, diag.Diagnostics) {
	if values == nil {
		values = map[string]string{}
	}
	return types.MapValueFrom(ctx, types.StringType, values)
}
