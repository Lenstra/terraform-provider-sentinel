package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	fwpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lenstra/terraform-provider-sentinel/internal/sentinel"
)

var _ datasource.DataSource = &PolicySetDataSource{}

// PolicySetDataSource reads a sentinel.hcl file and exposes it in the shape a
// tfe_policy / tfe_policy_set pair needs.
type PolicySetDataSource struct{}

func NewPolicySetDataSource() datasource.DataSource {
	return &PolicySetDataSource{}
}

type policySetDataSourceModel struct {
	Path              types.String           `tfsdk:"path"`
	ConfigPath        types.String           `tfsdk:"config_path"`
	Dir               types.String           `tfsdk:"dir"`
	Policies          map[string]policyModel `tfsdk:"policies"`
	PolicyNames       []string               `tfsdk:"policy_names"`
	EnforcementLevels map[string]string      `tfsdk:"enforcement_levels"`
	Modules           map[string]moduleModel `tfsdk:"modules"`
	Imports           map[string]importModel `tfsdk:"imports"`
}

type policyModel struct {
	Name             types.String      `tfsdk:"name"`
	Source           types.String      `tfsdk:"source"`
	Path             types.String      `tfsdk:"path"`
	EnforcementLevel types.String      `tfsdk:"enforcement_level"`
	Content          types.String      `tfsdk:"content"`
	Params           map[string]string `tfsdk:"params"`
}

type moduleModel struct {
	Name    types.String `tfsdk:"name"`
	Source  types.String `tfsdk:"source"`
	Path    types.String `tfsdk:"path"`
	Content types.String `tfsdk:"content"`
	Legacy  types.Bool   `tfsdk:"legacy"`
}

type importModel struct {
	Type   types.String      `tfsdk:"type"`
	Name   types.String      `tfsdk:"name"`
	Source types.String      `tfsdk:"source"`
	Format types.String      `tfsdk:"format"`
	Args   []string          `tfsdk:"args"`
	Env    map[string]string `tfsdk:"env"`
	Config map[string]string `tfsdk:"config"`
}

func (d *PolicySetDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policy_set"
}

func (d *PolicySetDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads a Sentinel configuration file (`sentinel.hcl`) and exposes its policies, " +
			"modules and imports so they can drive `tfe_policy` and `tfe_policy_set` resources.",
		Attributes: map[string]schema.Attribute{
			"path": schema.StringAttribute{
				Required: true,
				Description: "Path to a `sentinel.hcl` file, or to the directory holding one. " +
					"Relative paths are resolved against the Terraform working directory.",
			},
			"config_path": schema.StringAttribute{
				Computed:    true,
				Description: "Path of the configuration file that was read.",
			},
			"dir": schema.StringAttribute{
				Computed: true,
				Description: "Directory holding the configuration file. This is the value to pass to " +
					"`provider::sentinel::test`.",
			},
			"policy_names": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Names of every declared policy, sorted alphabetically.",
			},
			"enforcement_levels": schema.MapAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Enforcement level of each policy, keyed by policy name. A convenience view of `policies`.",
			},
			"policies": schema.MapNestedAttribute{
				Computed:    true,
				Description: "Declared policies, keyed by policy name so the map can be used directly in a `for_each`.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Name of the policy, as labelled in the configuration.",
						},
						"source": schema.StringAttribute{
							Computed:    true,
							Description: "Source of the policy, exactly as written in the configuration.",
						},
						"path": schema.StringAttribute{
							Computed: true,
							Description: "Path of the policy file, resolved against the configuration file. " +
								"Null when the source is not a local path.",
						},
						"enforcement_level": schema.StringAttribute{
							Computed: true,
							Description: "Enforcement level of the policy: `advisory`, `soft-mandatory` or " +
								"`hard-mandatory`. Defaults to `advisory` when the configuration omits it, " +
								"matching Sentinel. The value can be passed straight to `tfe_policy.enforce_mode`.",
						},
						"content": schema.StringAttribute{
							Computed: true,
							Description: "Content of the policy file, ready for `tfe_policy.policy`. " +
								"Null when the source is not a local path.",
						},
						"params": schema.MapAttribute{
							Computed:    true,
							ElementType: types.StringType,
							Description: "Parameters declared for the policy. Values that are not strings are JSON encoded.",
						},
					},
				},
			},
			"modules": schema.MapNestedAttribute{
				Computed: true,
				Description: "Declared modules, keyed by module name. Holds modules declared as " +
					"`import \"module\" \"name\"` as well as those declared with the older bare " +
					"`module \"name\"` block, since either way the configuration is naming a module.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Name of the module, as labelled in the configuration.",
						},
						"source": schema.StringAttribute{
							Computed:    true,
							Description: "Source of the module, exactly as written in the configuration.",
						},
						"path": schema.StringAttribute{
							Computed: true,
							Description: "Path of the module file, resolved against the configuration file. " +
								"Null when the source is not a local path.",
						},
						"content": schema.StringAttribute{
							Computed:    true,
							Description: "Content of the module file. Null when the source is not a local path.",
						},
						"legacy": schema.BoolAttribute{
							Computed: true,
							Description: "True when the module came from a bare `module` block rather than " +
								"from `import \"module\"`, which is the current form.",
						},
					},
				},
			},
			"imports": schema.MapNestedAttribute{
				Computed: true,
				Description: "Declared imports other than modules, keyed by `<type>.<name>` since Sentinel " +
					"labels an import block with both an import type and a name. Modules are reported " +
					"under `modules` instead.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Computed: true,
							Description: "Import type, the first label of the block: `plugin` for a standard " +
								"import or an import plugin, `static` for a data file.",
						},
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Import name, the second label of the block.",
						},
						"source": schema.StringAttribute{
							Computed: true,
							Description: "Source of the import. Null when the block declares none, which is " +
								"the case for a standard import the runtime already provides.",
						},
						"format": schema.StringAttribute{
							Computed: true,
							Description: "Format of a `static` import's data file. Null for any other " +
								"import type.",
						},
						"args": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
							Description: "Arguments passed to a `plugin` import's executable.",
						},
						"env": schema.MapAttribute{
							Computed:    true,
							ElementType: types.StringType,
							Description: "Environment variables set when launching a `plugin` import.",
						},
						"config": schema.MapAttribute{
							Computed:    true,
							ElementType: types.StringType,
							Description: "Configuration passed to the import. Values that are not strings are JSON encoded.",
						},
					},
				},
			},
		},
	}
}

func (d *PolicySetDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model policySetDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}

	configPath, err := resolveConfigPath(model.Path.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			fwpath.Root("path"),
			"Cannot locate the Sentinel configuration",
			err.Error(),
		)
		return
	}

	config, err := sentinel.ParseConfigFile(configPath)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the Sentinel configuration", err.Error())
		return
	}

	model.ConfigPath = types.StringValue(configPath)
	model.Dir = types.StringValue(config.Dir())
	model.Policies = map[string]policyModel{}
	model.EnforcementLevels = map[string]string{}
	model.PolicyNames = make([]string, 0, len(config.Policies))
	model.Modules = map[string]moduleModel{}
	model.Imports = map[string]importModel{}

	for _, policy := range config.Policies {
		path, content, err := readSource(config, policy.Source)
		if err != nil {
			resp.Diagnostics.AddError(
				fmt.Sprintf("Cannot read policy %q", policy.Name),
				err.Error(),
			)
			return
		}

		model.Policies[policy.Name] = policyModel{
			Name:             types.StringValue(policy.Name),
			Source:           types.StringValue(policy.Source),
			Path:             path,
			EnforcementLevel: types.StringValue(policy.EnforcementLevel),
			Content:          content,
			Params:           orEmptyMap(policy.Params),
		}
		model.EnforcementLevels[policy.Name] = policy.EnforcementLevel
		model.PolicyNames = append(model.PolicyNames, policy.Name)
	}
	sort.Strings(model.PolicyNames)

	for _, module := range config.Modules {
		path, content, err := readSource(config, module.Source)
		if err != nil {
			resp.Diagnostics.AddError(
				fmt.Sprintf("Cannot read module %q", module.Name),
				err.Error(),
			)
			return
		}

		model.Modules[module.Name] = moduleModel{
			Name:    types.StringValue(module.Name),
			Source:  types.StringValue(module.Source),
			Path:    path,
			Content: content,
			Legacy:  types.BoolValue(module.Legacy),
		}
	}

	for _, imp := range config.Imports {
		model.Imports[imp.Type+"."+imp.Name] = importModel{
			Type:   types.StringValue(imp.Type),
			Name:   types.StringValue(imp.Name),
			Source: orNullString(imp.Source),
			Format: orNullString(imp.Format),
			Args:   orEmptyList(imp.Args),
			Env:    orEmptyMap(imp.Env),
			Config: orEmptyMap(imp.Config),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

// resolveConfigPath accepts either the configuration file itself or the
// directory holding it, which is what users have at hand in either case.
func resolveConfigPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path must not be empty")
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %q: %w", path, err)
	}

	if !info.IsDir() {
		return filepath.Clean(path), nil
	}

	configPath := filepath.Join(path, sentinel.ConfigFileName)
	if _, err := os.Stat(configPath); err != nil {
		return "", fmt.Errorf("%q does not contain a %s file: %w", path, sentinel.ConfigFileName, err)
	}
	return configPath, nil
}

// readSource resolves a source declared in the configuration and reads it. A
// source that does not live on the local filesystem yields two null values
// rather than an error: it is valid Sentinel, there is just nothing to read.
func readSource(config *sentinel.Config, source string) (types.String, types.String, error) {
	path, local := config.ResolvePath(source)
	if !local {
		return types.StringNull(), types.StringNull(), nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return types.StringNull(), types.StringNull(), fmt.Errorf("cannot read %q: %w", path, err)
	}

	return types.StringValue(path), types.StringValue(string(content)), nil
}

// orEmptyMap keeps an absent params, env or config attribute from surfacing as a
// null map, so that configurations can always iterate over the attribute.
func orEmptyMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

// orEmptyList does the same for an absent list attribute.
func orEmptyList(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// orNullString reports an attribute the configuration did not set as null,
// rather than as an empty string that would be indistinguishable from one.
func orNullString(value string) types.String {
	if value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}
