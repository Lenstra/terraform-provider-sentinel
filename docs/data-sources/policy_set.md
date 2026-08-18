---
page_title: "sentinel_policy_set Data Source - sentinel"
subcategory: ""
description: |-
  Reads a Sentinel configuration file (sentinel.hcl) and exposes its policies, modules and imports so they can drive tfe_policy and tfe_policy_set resources.
---

# sentinel_policy_set (Data Source)

Reads a Sentinel configuration file (`sentinel.hcl`) and exposes its policies,
modules and imports so they can drive `tfe_policy` and `tfe_policy_set`
resources.

The point is to stop maintaining the policy list twice. `sentinel.hcl` already
names every policy and its enforcement level; this data source reads that file so
the Terraform configuration does not repeat it.

`enforcement_level` uses the same values as `tfe_policy.enforce_mode` for
Sentinel policies (`advisory`, `soft-mandatory`, `hard-mandatory`), so it can be
passed straight through. A policy that declares no level comes back as
`advisory`, which is the level Sentinel itself assumes.

No Sentinel binary is needed: the file is parsed by the provider.

## Example Usage

```terraform
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
```

## Schema

### Required

- `path` (String) Path to a `sentinel.hcl` file, or to the directory holding one.
  Relative paths are resolved against the Terraform working directory.

### Read-Only

- `config_path` (String) Path of the configuration file that was read.
- `dir` (String) Directory holding the configuration file. This is the value to
  pass to `provider::sentinel::test`.
- `enforcement_levels` (Map of String) Enforcement level of each policy, keyed by
  policy name. A convenience view of `policies`.
- `imports` (Attributes Map) Declared imports other than modules, keyed by
  `<type>.<name>` since Sentinel labels an import block with both an import type
  and a name. Modules are reported under `modules` instead.
  (see [below for nested schema](#nestedatt--imports))
- `modules` (Attributes Map) Declared modules, keyed by module name.
  (see [below for nested schema](#nestedatt--modules))
- `policies` (Attributes Map) Declared policies, keyed by policy name so the map
  can be used directly in a `for_each`.
  (see [below for nested schema](#nestedatt--policies))
- `policy_names` (List of String) Names of every declared policy, sorted
  alphabetically.

<a id="nestedatt--policies"></a>
### Nested Schema for `policies`

Read-Only:

- `content` (String) Content of the policy file, ready for `tfe_policy.policy`.
  Null when the source is not a local path.
- `enforcement_level` (String) Enforcement level of the policy: `advisory`,
  `soft-mandatory` or `hard-mandatory`. Defaults to `advisory` when the
  configuration omits it, matching Sentinel. The value can be passed straight to
  `tfe_policy.enforce_mode`.
- `name` (String) Name of the policy, as labelled in the configuration.
- `params` (Map of String) Parameters declared for the policy. Values that are
  not strings are JSON encoded.
- `path` (String) Path of the policy file, resolved against the configuration
  file. Null when the source is not a local path.
- `source` (String) Source of the policy, exactly as written in the
  configuration.

<a id="nestedatt--modules"></a>
### Nested Schema for `modules`

Holds modules declared as `import "module" "name"` as well as those declared with
the older bare `module "name"` block, since either way the configuration is
naming a module.

Read-Only:

- `content` (String) Content of the module file. Null when the source is not a
  local path.
- `legacy` (Boolean) True when the module came from a bare `module` block rather
  than from `import "module"`, which is the current form.
- `name` (String) Name of the module, as labelled in the configuration.
- `path` (String) Path of the module file, resolved against the configuration
  file. Null when the source is not a local path.
- `source` (String) Source of the module, exactly as written in the
  configuration.

<a id="nestedatt--imports"></a>
### Nested Schema for `imports`

Not every attribute applies to every import type: `format` belongs to a `static`
import, while `args`, `env` and `config` belong to a `plugin`.

Read-Only:

- `args` (List of String) Arguments passed to a `plugin` import's executable.
- `config` (Map of String) Configuration passed to the import. Values that are
  not strings are JSON encoded.
- `env` (Map of String) Environment variables set when launching a `plugin`
  import.
- `format` (String) Format of a `static` import's data file. Null for any other
  import type.
- `name` (String) Import name, the second label of the block.
- `source` (String) Source of the import. Null when the block declares none,
  which is the case for a standard import the runtime already provides.
- `type` (String) Import type, the first label of the block: `plugin` for a
  standard import or an import plugin, `static` for a data file.

## Notes

### Modules

Current Sentinel declares a module as a multi-label import block:

```hcl
import "module" "tfplan-functions" {
  source = "./modules/tfplan-functions.sentinel"
}
```

Older policy sets use a bare `module` block, which is still common:

```hcl
module "tfplan-functions" {
  source = "./modules/tfplan-functions.sentinel"
}
```

Both forms are read and both land in `modules`, so a `for_each` over the map does
not care which one the configuration uses. `legacy` tells them apart.

### Sources that are not local paths

Sentinel resolves a source over the network when it carries a forced protocol
prefix (`git::`, `hg::`, `http::`, `https::`, `s3::`, `gcp::`) or is a bare
`http://`, `https://`, `github.com/` or `bitbucket.org/` address. Such a policy or
module has no file to read, so its `path` and `content` come back null. Everything
else about it is still reported.

The `file::` prefix forces the local filesystem, and a `?checksum=` query is a
Sentinel feature rather than part of the filename; neither stops the file being
read.

### Blocks the provider does not model

`sentinel.hcl` may hold `mock`, `global`, `param`, `test` and `sentinel` blocks,
which configure a CLI run rather than the policy set. Those are ignored rather
than treated as an error, so a configuration written for the Sentinel CLI can be
read as it is.
