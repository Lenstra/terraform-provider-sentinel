# Same gate as provider::sentinel::test, but the error message says what broke.
# Calling the function from a local keeps it to a single Sentinel run per plan,
# even when several resources check the same result.
locals {
  tests = provider::sentinel::test_report("${path.module}/policies")
}

resource "tfe_policy_set" "this" {
  name         = "example"
  organization = var.organization
  kind         = "sentinel"

  lifecycle {
    precondition {
      condition = local.tests.passed
      error_message = join("\n", concat(
        ["${local.tests.failed_count} of ${local.tests.total} Sentinel tests fail:"],
        local.tests.failures,
      ))
    }
  }
}
