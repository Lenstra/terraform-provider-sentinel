---
page_title: "sentinel Provider"
subcategory: ""
description: |-
  Reads Sentinel policy configuration and runs Sentinel tests from Terraform.
---

# sentinel Provider

Bridges a [Sentinel](https://developer.hashicorp.com/sentinel) policy set on disk
and the Terraform resources that publish it.

It does two things:

- [`sentinel_policy_set`](data-sources/policy_set.md) reads a `sentinel.hcl` file
  and hands back its policies, modules and imports in the shape `tfe_policy` and
  `tfe_policy_set` want, so the policy list is not maintained twice.
- [`provider::sentinel::test`](functions/test.md) and
  [`provider::sentinel::test_report`](functions/test_report.md) run
  `sentinel test` and report whether it passed, so a `lifecycle` precondition can
  stop an apply that would publish a failing policy.

## Requirements

- Terraform 1.8 or later, for provider defined functions.
- The `sentinel` CLI, for the functions only. The data source parses
  `sentinel.hcl` itself and needs no binary.

The Sentinel CLI is not redistributable, so the provider does not bundle it. The
functions run whatever `sentinel` is in `PATH`. Set `SENTINEL_BINARY` to use a
different binary:

```shell
export SENTINEL_BINARY=/opt/sentinel/bin/sentinel
```

The CLI runs wherever Terraform runs. On a remote HCP Terraform or Terraform
Enterprise run, that means the agent image has to carry the binary; without it
the functions fail with an error rather than returning `false`.

## Example Usage

```terraform
terraform {
  required_version = ">= 1.8.0"

  required_providers {
    sentinel = {
      source = "kevineor/sentinel"
    }
  }
}

# The provider takes no configuration. The Sentinel CLI is looked up in PATH as
# `sentinel`, unless the SENTINEL_BINARY environment variable points elsewhere.
provider "sentinel" {}
```

Wiring a policy set into HCP Terraform, gated on its own tests:

```terraform
locals {
  policy_dir = "${path.module}/policies"
}

data "sentinel_policy_set" "this" {
  path = local.policy_dir
}

# Run the tests once and reuse the result.
locals {
  tests = provider::sentinel::test_report(local.policy_dir)
}

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
  organization = var.organization
  kind         = "sentinel"
  policy_ids   = [for policy in tfe_policy.this : policy.id]
}
```

## Schema

The provider takes no arguments. The only thing worth configuring would be the
location of the Sentinel CLI, but provider defined functions cannot read the
provider configuration, so `SENTINEL_BINARY` is used instead. A provider
argument would have silently applied to the data source alone.
