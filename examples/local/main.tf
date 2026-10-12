# A self-contained run: no HCP Terraform account needed, only Terraform >= 1.8
# and the sentinel binary in PATH.
#
#   terraform init
#   terraform apply
#
# Break a policy under ../policies and apply again: the precondition fails and
# lists what broke, instead of applying the change.

terraform {
  required_version = ">= 1.8.0"

  required_providers {
    sentinel = {
      source = "lenstra/sentinel"
    }
  }
}

locals {
  policy_dir = "${path.module}/../policies"
}

data "sentinel_policy_set" "this" {
  path = local.policy_dir
}

locals {
  tests = provider::sentinel::test_report(local.policy_dir)
}

# Stands in for whatever resource would carry the policies. The precondition is
# the point: it refuses to move while the tests are red.
resource "terraform_data" "policy_set" {
  input = data.sentinel_policy_set.this.enforcement_levels

  lifecycle {
    precondition {
      condition = local.tests.passed
      error_message = join("\n", concat(
        ["The Sentinel tests fail, so the policy set is not applied:"],
        local.tests.failures,
      ))
    }
  }
}

output "policy_names" {
  description = "Policies found in sentinel.hcl."
  value       = data.sentinel_policy_set.this.policy_names
}

output "enforcement_levels" {
  description = "Enforcement level of each policy."
  value       = data.sentinel_policy_set.this.enforcement_levels
}

output "test_summary" {
  description = "How the Sentinel test run went."
  value = {
    passed = local.tests.passed
    total  = local.tests.total
    failed = local.tests.failed_count
  }
}
