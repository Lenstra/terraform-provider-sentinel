# Refuse to apply a policy set whose own tests fail.
resource "tfe_policy_set" "this" {
  name         = "example"
  organization = var.organization
  kind         = "sentinel"

  lifecycle {
    precondition {
      condition     = provider::sentinel::test("${path.module}/policies")
      error_message = "The Sentinel tests fail, run `sentinel test` in policies/ to see why."
    }
  }
}
