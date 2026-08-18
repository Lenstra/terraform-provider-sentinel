package provider

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/kevineor/terraform-provider-sentinel/internal/sentinel"
	"github.com/kevineor/terraform-provider-sentinel/internal/sentineltest"
)

// runFunction calls a function the way the framework does. result must be an
// unknown value of the function's return type.
func runFunction(t *testing.T, fn function.Function, dir string, result attr.Value) (attr.Value, *function.FuncError) {
	t.Helper()

	ctx := context.Background()
	req := function.RunRequest{
		Arguments: function.NewArgumentsData([]attr.Value{types.StringValue(dir)}),
	}
	resp := &function.RunResponse{Result: function.NewResultData(result)}

	fn.Run(ctx, req, resp)

	return resp.Result.Value(), resp.Error
}

func TestTestFunctionMetadata(t *testing.T) {
	ctx := context.Background()

	resp := &function.MetadataResponse{}
	NewTestFunction().Metadata(ctx, function.MetadataRequest{}, resp)

	if resp.Name != "test" {
		t.Errorf("Name = %q, want %q", resp.Name, "test")
	}
}

func TestTestFunctionDefinition(t *testing.T) {
	ctx := context.Background()

	resp := &function.DefinitionResponse{}
	NewTestFunction().Definition(ctx, function.DefinitionRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Definition: %s", resp.Diagnostics)
	}
	if got := len(resp.Definition.Parameters); got != 1 {
		t.Fatalf("got %d parameters, want 1", got)
	}
	if _, ok := resp.Definition.Return.(function.BoolReturn); !ok {
		t.Errorf("Return is %T, want function.BoolReturn", resp.Definition.Return)
	}
}

func TestTestFunctionRun(t *testing.T) {
	tests := map[string]struct {
		report   string
		exitCode int
		want     bool
	}{
		"passing tests":            {report: sentineltest.PassingReport, exitCode: 0, want: true},
		"failing tests":            {report: sentineltest.FailingReport, exitCode: 1, want: false},
		"unparseable output":       {report: "PASS - a.sentinel\n", exitCode: 0, want: true},
		"unparseable output, fail": {report: "FAIL - a.sentinel\n", exitCode: 1, want: false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			dir := sentineltest.PolicyDir(t)
			cli := sentineltest.NewCLI(t, test.report, "", test.exitCode)
			t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

			value, funcErr := runFunction(t, NewTestFunction(), dir, types.BoolUnknown())
			if funcErr != nil {
				t.Fatalf("Run: %s", funcErr)
			}

			got, ok := value.(types.Bool)
			if !ok {
				t.Fatalf("result is %T, want types.Bool", value)
			}
			if got.ValueBool() != test.want {
				t.Errorf("result = %t, want %t", got.ValueBool(), test.want)
			}

			// The function must run the CLI in the directory it was given.
			args, cwd := cli.Invocation(t)
			if args != "test -json" {
				t.Errorf("args = %q, want %q", args, "test -json")
			}
			if want := sentineltest.ResolveDir(t, dir); cwd != want {
				t.Errorf("working directory = %q, want %q", cwd, want)
			}
		})
	}
}

// TestTestFunctionRunErrors covers setups that are broken rather than failing:
// they must surface as an error, never as a false result, so that a missing
// binary is not mistaken for a failing policy.
func TestTestFunctionRunErrors(t *testing.T) {
	t.Run("directory without a config file", func(t *testing.T) {
		cli := sentineltest.NewCLI(t, sentineltest.PassingReport, "", 0)
		t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

		_, funcErr := runFunction(t, NewTestFunction(), t.TempDir(), types.BoolUnknown())
		if funcErr == nil {
			t.Fatal("Run succeeded, want an error")
		}
		if !strings.Contains(funcErr.Text, "does not contain a sentinel.hcl file") {
			t.Errorf("error = %q", funcErr.Text)
		}
	})

	t.Run("missing binary", func(t *testing.T) {
		dir := sentineltest.PolicyDir(t)
		// An empty PATH and no override leaves nothing to run.
		t.Setenv("PATH", t.TempDir())
		t.Setenv(sentinel.BinaryEnvVar, "")

		_, funcErr := runFunction(t, NewTestFunction(), dir, types.BoolUnknown())
		if funcErr == nil {
			t.Fatal("Run succeeded, want an error")
		}
		if !strings.Contains(funcErr.Text, sentinel.BinaryEnvVar) {
			t.Errorf("error = %q, want it to mention %s", funcErr.Text, sentinel.BinaryEnvVar)
		}
	})

	t.Run("path is a file", func(t *testing.T) {
		dir := sentineltest.PolicyDir(t)
		cli := sentineltest.NewCLI(t, sentineltest.PassingReport, "", 0)
		t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

		_, funcErr := runFunction(t, NewTestFunction(), filepath.Join(dir, "sentinel.hcl"), types.BoolUnknown())
		if funcErr == nil {
			t.Fatal("Run succeeded, want an error")
		}
		if !strings.Contains(funcErr.Text, "is not a directory") {
			t.Errorf("error = %q", funcErr.Text)
		}
	})
}
