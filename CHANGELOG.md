## 0.0.2

NOTES:

- The provider moves to the Lenstra namespace: use `source = "Lenstra/sentinel"`
  instead of `kevineor/sentinel`. No other change.

## 0.0.1

FEATURES:

- **New Data Source:** `sentinel_policy_set` reads a `sentinel.hcl` policy set and
  exposes its policies, modules and imports.
- **New Function:** `provider::sentinel::test` runs `sentinel test` and reports
  whether it passed.
- **New Function:** `provider::sentinel::test_report` runs `sentinel test` and
  returns the per-case detail, for a useful precondition message.
