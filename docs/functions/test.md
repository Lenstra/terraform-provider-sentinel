---
page_title: "test function - sentinel"
subcategory: ""
description: |-
  Run the Sentinel tests of a policy set
---

# function: test

Runs `sentinel test` in the given directory and returns `true` when every test
passes. A directory whose policies have no tests passes, matching the Sentinel
CLI.

Failing to run the CLI at all, for instance because the binary is missing or the
directory holds no `sentinel.hcl`, returns an error rather than `false`, so a
broken setup is never mistaken for a failing policy.

The CLI is looked up in `PATH` as `sentinel`, unless the `SENTINEL_BINARY`
environment variable points somewhere else.

## Example Usage

```terraform
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
```

## Signature

```text
test(dir string) bool
```

## Arguments

1. `dir` (String) Directory holding the `sentinel.hcl` file and the tests to run.

## Notes

### Where the tests run

The CLI runs with `dir` as its working directory, because Sentinel resolves
policy sources, modules and test files relative to `sentinel.hcl`. `dir` must be
the directory holding that file, not the file itself.

`sentinel_policy_set` exposes the right directory as `dir`, which is handy when
the path is already computed:

```terraform
provider::sentinel::test(data.sentinel_policy_set.this.dir)
```

### One run per plan

The function runs the CLI on every evaluation. When several resources gate on the
same result, call it once from a `locals` block and reference that:

```terraform
locals {
  tests_pass = provider::sentinel::test("${path.module}/policies")
}
```

### The result has to be reproducible

Terraform calls a provider defined function again during apply and fails the run
if it returns anything different from what it returned at plan time. A boolean
over unchanged policies is stable, so this only bites if the policies are edited
between a saved plan and its apply, which is the intended outcome: the plan was
approved against different policies.

### Getting the failure detail

`true`/`false` says nothing about what broke. Use
[`test_report`](test_report.md) when the precondition message should list the
failing tests.
