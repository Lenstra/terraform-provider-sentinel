package provider

import (
	"fmt"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
	"github.com/kevineor/terraform-provider-sentinel/internal/sentinel"
	"github.com/kevineor/terraform-provider-sentinel/internal/sentineltest"
)

// These tests run the real Terraform CLI, so they only run when TF_ACC is set.
// The Sentinel CLI is stubbed out through SENTINEL_BINARY: the provider server
// runs in this process, so it picks up the environment the test sets.

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"sentinel": providerserver.NewProtocol6WithError(New("test")()),
}

// hclPath quotes a path for use in a Terraform configuration.
func hclPath(t *testing.T, path string) string {
	t.Helper()

	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolving %s: %s", path, err)
	}
	return filepath.ToSlash(absolute)
}

func TestAccPolicySetDataSource(t *testing.T) {
	path := hclPath(t, filepath.Join("testdata", "policy-set"))

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
data "sentinel_policy_set" "test" {
  path = %q
}

output "policy_names" {
  value = join(",", data.sentinel_policy_set.test.policy_names)
}

# Imports are keyed by "<type>.<name>", which is awkward to assert on as a state
# address, so they go through outputs instead.
output "import_type" {
  value = data.sentinel_policy_set.test.imports["plugin.time"].type
}

output "static_format" {
  value = data.sentinel_policy_set.test.imports["static.people"].format
}
`, path),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.sentinel_policy_set.test", "policy_names.#", "3"),
				resource.TestCheckResourceAttr(
					"data.sentinel_policy_set.test",
					"policies.restrict-instance-type.enforcement_level",
					"hard-mandatory",
				),
				// A policy that declares no level falls back to advisory.
				resource.TestCheckResourceAttr(
					"data.sentinel_policy_set.test",
					"policies.require-tags.enforcement_level",
					"advisory",
				),
				// A source that is not a local path has no content to read.
				resource.TestCheckNoResourceAttr(
					"data.sentinel_policy_set.test",
					"policies.shared-policy.content",
				),
				resource.TestCheckResourceAttr(
					"data.sentinel_policy_set.test",
					"enforcement_levels.shared-policy",
					"soft-mandatory",
				),
				// `import "module"` and the older bare `module` block both land
				// in modules.
				resource.TestCheckResourceAttr("data.sentinel_policy_set.test", "modules.helpers.legacy", "false"),
				resource.TestCheckResourceAttr("data.sentinel_policy_set.test", "modules.legacy.legacy", "true"),
				resource.TestCheckOutput("policy_names", "require-tags,restrict-instance-type,shared-policy"),
				resource.TestCheckOutput("import_type", "plugin"),
				resource.TestCheckOutput("static_format", "json"),
			),
		}},
	})
}

func TestAccTestFunction(t *testing.T) {
	tests := map[string]struct {
		report   string
		exitCode int
		want     string
	}{
		"passing": {report: sentineltest.PassingReport, exitCode: 0, want: "true"},
		"failing": {report: sentineltest.FailingReport, exitCode: 1, want: "false"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			dir := sentineltest.PolicyDir(t)
			cli := sentineltest.NewCLI(t, test.report, "", test.exitCode)
			t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

			resource.Test(t, resource.TestCase{
				TerraformVersionChecks: []tfversion.TerraformVersionCheck{
					// Provider defined functions need Terraform 1.8.
					tfversion.SkipBelow(tfversion.Version1_8_0),
				},
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config: fmt.Sprintf(`
output "passed" {
  value = provider::sentinel::test(%q)
}
`, hclPath(t, dir)),
					Check: resource.TestCheckOutput("passed", test.want),
				}},
			})
		})
	}
}

func TestAccTestReportFunction(t *testing.T) {
	dir := sentineltest.PolicyDir(t)
	cli := sentineltest.NewCLI(t, sentineltest.FailingReport, "", 1)
	t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
locals {
  report = provider::sentinel::test_report(%q)
}

output "passed" {
  value = local.report.passed
}

output "failed_count" {
  value = local.report.failed_count
}

output "failures" {
  value = join("\n", local.report.failures)
}
`, hclPath(t, dir)),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckOutput("passed", "false"),
				resource.TestCheckOutput("failed_count", "1"),
				resource.TestCheckOutput(
					"failures",
					`require-tags.sentinel: test/require-tags/pass.hcl: FAIL: `+
						`main: expected "main" to be true, got: false`,
				),
			),
		}},
	})
}

// TestAccTestFunctionErrorsOnMissingConfig checks that a broken setup surfaces as
// a Terraform error, not as a false result that would look like a failing policy.
func TestAccTestFunctionErrorsOnMissingConfig(t *testing.T) {
	dir := t.TempDir()
	cli := sentineltest.NewCLI(t, sentineltest.PassingReport, "", 0)
	t.Setenv(sentinel.BinaryEnvVar, cli.Binary)

	resource.Test(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
output "passed" {
  value = provider::sentinel::test(%q)
}
`, hclPath(t, dir)),
			// Terraform wraps a diagnostic across lines, so the pattern only
			// matches the tail of the sentence.
			ExpectError: regexp.MustCompile(`contain a sentinel\.hcl file`),
		}},
	})
}
