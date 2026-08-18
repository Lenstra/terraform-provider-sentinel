---
page_title: "test_report function - sentinel"
subcategory: ""
description: |-
  Run the Sentinel tests of a policy set and return the detailed results
---

# function: test_report

Runs `sentinel test` in the given directory and returns an object describing the
run.

Use this instead of [`test`](test.md) when the precondition should say *what*
failed. `passed` carries the same value `test` returns.

## Example Usage

```terraform
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
```

## Signature

```text
test_report(dir string) object
```

## Arguments

1. `dir` (String) Directory holding the `sentinel.hcl` file and the tests to run.

## Return Type

The returned object has these attributes:

- `passed` (Bool) Whether every test passed. The same value
  [`test`](test.md) returns.
- `exit_code` (Number) Exit code of the CLI.
- `total` (Number) Number of test cases run. A policy with no cases counts as one
  entry, so a policy that failed to parse is still visible in the totals.
- `passed_count` (Number) Number of cases that passed.
- `failed_count` (Number) Number of cases that failed.
- `failures` (List of String) One line per failure. A failing case reads
  `<policy>: <case>: <status>: <messages>; failed rules: <rules>`; a policy that
  could not be processed at all reads `<policy>: <status>: <messages>`. Empty when
  nothing failed.
- `policies` (List of Object) Per policy results, sorted by path. Each entry has:
  - `path` (String) Path of the policy file.
  - `status` (String) One of `PASS`, `FAIL`, `ERROR` or `?`, the last meaning the
    policy had no test cases, which Sentinel treats as passing.
  - `errors` (List of String) Messages from processing the policy itself,
    typically parser errors, as opposed to a case failing.
  - `cases` (List of Object) The policy's test cases, sorted by path, each with:
    - `path` (String) Path of the test case file.
    - `status` (String) One of `PASS`, `FAIL` or `ERROR`.
    - `errors` (List of String) Messages from running this case.
    - `rule_failures` (Map of String) Maps the name of a rule whose assertion
      failed to why it failed, for instance `main` to
      `expected "main" to be true, got: false`. Sentinel only reports failures
      here, so a rule absent from the map either passed or was not asserted.
      This is where a failing case explains itself: `errors` stays empty for an
      assertion failure.
- `decoded` (Boolean) Whether the JSON report could be read. When `false`, only
  `passed`, `exit_code` and `failures` carry information.

## Notes

### The result has to be reproducible

Terraform calls a provider defined function again during apply and fails the run
if it returns anything different from what it returned at plan time. That is why
the returned object carries neither the run time the CLI reports nor its raw
output: both embed timings, and an apply would fail with *function returned an
inconsistent result*.

The raw output goes to the provider log instead:

```shell
TF_LOG=debug terraform plan
```

The same constraint applies to the policies themselves. Editing them between a
saved plan and its apply changes the result and fails the apply, which is the
intended outcome: the plan was approved against different policies.

### How the result is determined

`passed` and `exit_code` come from the CLI exit status. Everything else comes from
`sentinel test -json`.

Sentinel documents that JSON format as subject to change. When it cannot be
decoded, `decoded` comes back `false`, `policies` is empty, the counts are zero,
and `failures` holds one line pointing at the directory to run `sentinel test` in.
The gate itself still works, off the exit code.

`passed` requires both a zero exit code and a report with no failure in it. That
fails closed: since this gates an apply, a disagreement between the two must never
let a failing policy through.

### Errors versus failures

A run that completes with failing tests returns a report with `passed` set to
`false`. Failing to run the CLI at all, a missing binary or a directory with no
`sentinel.hcl`, returns an error instead, so a broken setup is never mistaken for
a failing policy.

Note the third case: a policy the CLI could not parse comes back inside the
report, as a policy with status `ERROR`, no cases and a message in `errors`. It
counts as a failure.
