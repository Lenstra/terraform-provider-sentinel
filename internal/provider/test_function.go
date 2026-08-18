package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/kevineor/terraform-provider-sentinel/internal/sentinel"
)

var _ function.Function = &TestFunction{}

// TestFunction runs `sentinel test` and reports whether it passed. It is meant
// for a `lifecycle` precondition, so that a policy set whose tests fail is never
// applied.
type TestFunction struct{}

func NewTestFunction() function.Function {
	return &TestFunction{}
}

func (f *TestFunction) Metadata(ctx context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "test"
}

func (f *TestFunction) Definition(ctx context.Context, req function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Run the Sentinel tests of a policy set",
		Description: "Runs `sentinel test` in the given directory and returns `true` when every test passes. " +
			"A directory whose policies have no tests passes, matching the Sentinel CLI.\n\n" +
			"Failing to run the CLI at all, for instance because the binary is missing or the directory holds no " +
			"`sentinel.hcl`, returns an error rather than `false`, so a broken setup is never mistaken for a " +
			"failing policy.\n\n" +
			"The CLI is looked up in `PATH` as `sentinel`, unless the `SENTINEL_BINARY` environment variable points " +
			"somewhere else.",
		Parameters: []function.Parameter{
			function.StringParameter{
				Name:        "dir",
				Description: "Directory holding the `sentinel.hcl` file and the tests to run.",
			},
		},
		Return: function.BoolReturn{},
	}
}

func (f *TestFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
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

	resp.Error = function.ConcatFuncErrors(resp.Error, resp.Result.Set(ctx, report.Passed))
}
