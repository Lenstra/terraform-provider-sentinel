# Either the directory holding sentinel.hcl, or the file itself.
data "sentinel_policy_set" "this" {
  path = "${path.module}/policies"
}

# The map is keyed by policy name, so it drops straight into a for_each.
resource "tfe_policy" "this" {
  for_each = data.sentinel_policy_set.this.policies

  name         = each.value.name
  organization = var.organization
  kind         = "sentinel"
  enforce_mode = each.value.enforcement_level
  policy       = each.value.content
}

resource "tfe_policy_set" "this" {
  name         = "example"
  organization = var.organization
  kind         = "sentinel"
  policy_ids   = [for policy in tfe_policy.this : policy.id]
}
