# Turns a Sentinel policy set on disk into a policy set in HCP Terraform, and
# refuses to apply it while its own tests fail.

terraform {
  required_version = ">= 1.8.0"

  required_providers {
    sentinel = {
      source = "kevineor/sentinel"
    }
    tfe = {
      source = "hashicorp/tfe"
    }
  }
}

variable "organization" {
  type        = string
  description = "HCP Terraform organization that owns the policy set."
}

variable "workspace_ids" {
  type        = list(string)
  description = "Workspaces the policy set applies to. Leave empty to attach it to none."
  default     = []
}

locals {
  policy_dir = "${path.module}/../policies"
}

# Everything sentinel.hcl declares, in the shape the tfe resources want.
data "sentinel_policy_set" "this" {
  path = local.policy_dir
}

# Run the tests once and reuse the result. Putting the call in a local rather
# than in each precondition keeps it to a single Sentinel run per plan.
locals {
  tests = provider::sentinel::test_report(local.policy_dir)
}

# One tfe_policy per policy declared in sentinel.hcl. enforcement_level maps
# directly onto enforce_mode for Sentinel policies.
resource "tfe_policy" "this" {
  for_each = data.sentinel_policy_set.this.policies

  name         = each.value.name
  organization = var.organization
  kind         = "sentinel"
  enforce_mode = each.value.enforcement_level
  policy       = each.value.content

  lifecycle {
    precondition {
      condition = local.tests.passed
      error_message = join("\n", concat(
        ["The Sentinel tests fail, so the policies are not applied:"],
        local.tests.failures,
      ))
    }
  }
}

resource "tfe_policy_set" "this" {
  name         = "example"
  description  = "Built from ${basename(data.sentinel_policy_set.this.config_path)}"
  organization = var.organization
  kind         = "sentinel"

  policy_ids    = [for policy in tfe_policy.this : policy.id]
  workspace_ids = var.workspace_ids
}

output "policies" {
  description = "Enforcement level of each policy that was applied."
  value       = data.sentinel_policy_set.this.enforcement_levels
}
