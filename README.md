# terraform-provider-sentinel

A Terraform provider that bridges a [Sentinel](https://developer.hashicorp.com/sentinel)
policy set on disk and the Terraform resources that publish it.

Same idea as [lenstra/promtool](https://github.com/Lenstra/terraform-provider-promtool):
a provider defined function that validates something, called from a `lifecycle`
precondition so a broken configuration is never applied.

## What it does

**`data "sentinel_policy_set"`** reads a `sentinel.hcl` file and hands back its
policies, modules and imports in the shape `tfe_policy` and `tfe_policy_set` want.
`sentinel.hcl` already names every policy and its enforcement level, so the
Terraform configuration stops repeating it.

**`provider::sentinel::test(dir)`** runs `sentinel test` and returns `true` or
`false`, for a `lifecycle` precondition that refuses to apply a policy whose own
tests fail.

**`provider::sentinel::test_report(dir)`** runs the same tests but returns the
detail, so the precondition message can list what broke instead of only refusing.

## Usage

```hcl
terraform {
  required_version = ">= 1.8.0"

  required_providers {
    sentinel = { source = "kevineor/sentinel" }
    tfe      = { source = "hashicorp/tfe" }
  }
}

locals {
  policy_dir = "${path.module}/policies"
}

data "sentinel_policy_set" "this" {
  path = local.policy_dir
}

# Run the tests once and reuse the result across every precondition.
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

`enforcement_level` uses the same values as `tfe_policy.enforce_mode` for Sentinel
policies (`advisory`, `soft-mandatory`, `hard-mandatory`), so it passes straight
through. A policy that declares no level reads back as `advisory`, the level
Sentinel itself assumes.

## Requirements

- Terraform 1.8 or later, for provider defined functions.
- Go 1.25.8 or later, to build. That floor comes from `terraform-plugin-testing`.
- The `sentinel` CLI, for the functions only. The data source parses
  `sentinel.hcl` itself and needs no binary.

The Sentinel CLI is not redistributable, so the provider does not bundle it. The
functions run whatever `sentinel` is in `PATH`; set `SENTINEL_BINARY` to point
elsewhere. The CLI runs wherever Terraform runs, so a remote HCP Terraform or
Terraform Enterprise run needs the binary in its agent image.

The provider itself takes no configuration. The one thing worth configuring would
be the CLI location, but provider defined functions cannot read the provider
configuration, so `SENTINEL_BINARY` covers it. A provider argument would have
silently applied to the data source alone.

## Layout

```
main.go                        provider entry point
internal/sentinel/             sentinel.hcl parsing and CLI execution, no Terraform
  config.go                    parses policy, module and import blocks
  runner.go                    runs `sentinel test -json` and reads the report
internal/provider/             the Terraform provider
  policy_set_data_source.go    data "sentinel_policy_set"
  test_function.go             provider::sentinel::test
  test_report_function.go      provider::sentinel::test_report
internal/sentineltest/         fake Sentinel CLI used by the tests
docs/                          registry documentation
examples/
  policies/                    a Sentinel policy set with passing tests
  local/                       runnable demo, no HCP Terraform account needed
  tfe-policy-set/              the full tfe_policy / tfe_policy_set wiring
```

## Trying it out

`examples/local` runs against `examples/policies` and needs no HCP Terraform
account, only Terraform and the `sentinel` binary.

Build and install the provider locally, then point Terraform at it with a
[dev override](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides):

```shell
make install
```

```hcl
# ~/.terraformrc
provider_installation {
  dev_overrides {
    "kevineor/sentinel" = "/path/to/your/GOBIN"
  }
  direct {}
}
```

```shell
cd examples/local
terraform apply
```

Then break one of the policies under `examples/policies` and apply again: the
precondition fails and lists the failing tests instead of applying.

## Development

```shell
make build      # compile
make test       # unit tests
make testacc    # acceptance tests, needs terraform
make fmt        # gofmt
make lint       # go vet
```

The unit tests do not need the Sentinel binary: `internal/sentineltest` provides a
fake CLI that replays `-json` reports captured from a real run, since the real
binary cannot be shipped as a fixture. The acceptance tests need a `terraform`
binary but reuse that same fake through `SENTINEL_BINARY`, so they do not need
Sentinel either.

The exception is `internal/sentinel/integration_test.go`, which drives the real
`sentinel` binary and skips when it is missing. Those tests are the only place the
assumptions about the CLI are actually checked, so run them with Sentinel
installed before trusting a change to the runner:

```shell
sentinel version && go test ./internal/sentinel/ -run TestIntegration -v
```

`docs/` is written by hand. Switching to
[tfplugindocs](https://github.com/hashicorp/terraform-plugin-docs) would need
`templates/` to keep the prose sections, since generation overwrites the files.

## Notes on the Sentinel interface

`sentinel test -json` is the source of the detailed report. Sentinel documents that
format as subject to change, so the runner treats it as best effort: the exit code
decides whether the gate passes, and a report that cannot be decoded degrades to
the raw CLI output rather than failing the run.

`passed` requires both a zero exit code and a report with no failure in it. That
fails closed, which is the behaviour a policy gate wants.

A failing assertion explains itself through the report's `rule_detail`, which maps
a rule to why it failed, and leaves the case's `errors` empty. That message is the
useful half of a precondition failure, so it is what `failures` carries.

Terraform calls a provider defined function again during apply and fails the run
if it returns anything different from what it returned at plan time. So
`test_report` returns neither the run time nor the raw CLI output, which both
embed timings; the raw output goes to the provider log, visible with
`TF_LOG=debug`.

`sentinel.hcl` may hold `mock`, `global`, `param`, `test` and `sentinel` blocks,
which configure a CLI run rather than the policy set. The parser ignores those
rather than erroring, so a configuration written for the CLI can be read as it is.

Modules come in two forms. Current Sentinel writes
`import "module" "name" { source = ... }`; older policy sets write a bare
`module "name" { ... }`, which is still common. Both are read and both land in
`modules`, with a `legacy` flag telling them apart, so a `for_each` over the map
does not care which form the configuration uses.
