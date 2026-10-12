## Unreleased

BREAKING CHANGES:

- The provider moves from `kevineor/sentinel` to `lenstra/sentinel` on the
  Terraform Registry. Update the `source` in `required_providers` and run
  `terraform state replace-provider registry.terraform.io/kevineor/sentinel
  registry.terraform.io/lenstra/sentinel` (see the README).

## 0.0.1

FEATURES:

- **New Data Source:** `sentinel_policy_set` reads a `sentinel.hcl` policy set and
  exposes its policies, modules and imports.
- **New Function:** `provider::sentinel::test` runs `sentinel test` and reports
  whether it passed.
- **New Function:** `provider::sentinel::test_report` runs `sentinel test` and
  returns the per-case detail, for a useful precondition message.
