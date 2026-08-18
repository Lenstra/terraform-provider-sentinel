package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// readPolicySet drives the data source's Read the way the framework does, so the
// schema and the state mapping are exercised without needing a Terraform binary.
func readPolicySet(t *testing.T, path string) (policySetDataSourceModel, diag.Diagnostics) {
	t.Helper()

	ctx := context.Background()
	dataSource := NewPolicySetDataSource()

	schemaResp := &datasource.SchemaResponse{}
	dataSource.Schema(ctx, datasource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("Schema: %s", schemaResp.Diagnostics)
	}
	dataSourceSchema := schemaResp.Schema

	objectType, ok := dataSourceSchema.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatalf("the schema is not an object type, got %T", dataSourceSchema.Type().TerraformType(ctx))
	}

	// Only "path" is set in the configuration; every computed attribute starts
	// out null, as it would on a real read.
	attributes := make(map[string]tftypes.Value, len(objectType.AttributeTypes))
	for name, attributeType := range objectType.AttributeTypes {
		attributes[name] = tftypes.NewValue(attributeType, nil)
	}
	attributes["path"] = tftypes.NewValue(tftypes.String, path)
	raw := tftypes.NewValue(objectType, attributes)

	req := datasource.ReadRequest{Config: tfsdk.Config{Schema: dataSourceSchema, Raw: raw}}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: dataSourceSchema, Raw: raw}}
	dataSource.Read(ctx, req, resp)

	var model policySetDataSourceModel
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Get(ctx, &model)...)
	}

	return model, resp.Diagnostics
}

func TestPolicySetDataSourceRead(t *testing.T) {
	dir := filepath.Join("testdata", "policy-set")

	// The data source accepts either the directory or the file itself, since
	// that is what users have at hand in either case.
	for _, path := range []string{dir, filepath.Join(dir, "sentinel.hcl")} {
		t.Run(path, func(t *testing.T) {
			model, diags := readPolicySet(t, path)
			if diags.HasError() {
				t.Fatalf("Read: %s", diags)
			}

			if want := filepath.Join(dir, "sentinel.hcl"); model.ConfigPath.ValueString() != want {
				t.Errorf("config_path = %q, want %q", model.ConfigPath.ValueString(), want)
			}
			if model.Dir.ValueString() != dir {
				t.Errorf("dir = %q, want %q", model.Dir.ValueString(), dir)
			}

			wantNames := []string{"require-tags", "restrict-instance-type", "shared-policy"}
			if diff := cmp.Diff(wantNames, model.PolicyNames); diff != "" {
				t.Errorf("policy_names mismatch (-want +got):\n%s", diff)
			}

			wantLevels := map[string]string{
				"require-tags":           "advisory",
				"restrict-instance-type": "hard-mandatory",
				"shared-policy":          "soft-mandatory",
			}
			if diff := cmp.Diff(wantLevels, model.EnforcementLevels); diff != "" {
				t.Errorf("enforcement_levels mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPolicySetDataSourceReadPolicies(t *testing.T) {
	dir := filepath.Join("testdata", "policy-set")

	model, diags := readPolicySet(t, dir)
	if diags.HasError() {
		t.Fatalf("Read: %s", diags)
	}

	restrictPath := filepath.Join(dir, "restrict-instance-type.sentinel")
	restrictContent := readTestFile(t, restrictPath)
	requirePath := filepath.Join(dir, "require-tags.sentinel")
	requireContent := readTestFile(t, requirePath)

	want := map[string]policyModel{
		"restrict-instance-type": {
			Name:             types.StringValue("restrict-instance-type"),
			Source:           types.StringValue("./restrict-instance-type.sentinel"),
			Path:             types.StringValue(restrictPath),
			EnforcementLevel: types.StringValue("hard-mandatory"),
			Content:          types.StringValue(restrictContent),
			Params: map[string]string{
				// Non-string params are JSON encoded so the map stays a
				// map(string).
				"allowed": `["t3.micro","t3.small"]`,
				"max":     "2",
			},
		},
		"require-tags": {
			Name:             types.StringValue("require-tags"),
			Source:           types.StringValue("./require-tags.sentinel"),
			Path:             types.StringValue(requirePath),
			EnforcementLevel: types.StringValue("advisory"),
			Content:          types.StringValue(requireContent),
			Params:           map[string]string{},
		},
		"shared-policy": {
			Name:   types.StringValue("shared-policy"),
			Source: types.StringValue("git::https://github.com/hashicorp/sentinel-example.git//main.sentinel"),
			// A source that is not a local path has nothing to read.
			Path:             types.StringNull(),
			EnforcementLevel: types.StringValue("soft-mandatory"),
			Content:          types.StringNull(),
			Params:           map[string]string{},
		},
	}
	if diff := cmp.Diff(want, model.Policies); diff != "" {
		t.Errorf("policies mismatch (-want +got):\n%s", diff)
	}
}

func TestPolicySetDataSourceReadModulesAndImports(t *testing.T) {
	dir := filepath.Join("testdata", "policy-set")

	model, diags := readPolicySet(t, dir)
	if diags.HasError() {
		t.Fatalf("Read: %s", diags)
	}

	helpersPath := filepath.Join(dir, "modules", "helpers.sentinel")
	legacyPath := filepath.Join(dir, "modules", "legacy.sentinel")

	// Both `import "module"` and the older bare `module` block are reported as
	// modules; `legacy` says which form the configuration used.
	wantModules := map[string]moduleModel{
		"helpers": {
			Name:    types.StringValue("helpers"),
			Source:  types.StringValue("./modules/helpers.sentinel"),
			Path:    types.StringValue(helpersPath),
			Content: types.StringValue(readTestFile(t, helpersPath)),
			Legacy:  types.BoolValue(false),
		},
		"legacy": {
			Name:    types.StringValue("legacy"),
			Source:  types.StringValue("./modules/legacy.sentinel"),
			Path:    types.StringValue(legacyPath),
			Content: types.StringValue(readTestFile(t, legacyPath)),
			Legacy:  types.BoolValue(true),
		},
	}
	if diff := cmp.Diff(wantModules, model.Modules); diff != "" {
		t.Errorf("modules mismatch (-want +got):\n%s", diff)
	}

	// Imports hold everything that is not a module, keyed by "<type>.<name>"
	// since Sentinel labels the block with both.
	wantImports := map[string]importModel{
		"plugin.time": {
			Type: types.StringValue("plugin"),
			Name: types.StringValue("time"),
			// A standard import declares no source.
			Source: types.StringNull(),
			Format: types.StringNull(),
			Args:   []string{},
			Env:    map[string]string{},
			Config: map[string]string{"timezone": "Europe/Paris"},
		},
		"static.people": {
			Type:   types.StringValue("static"),
			Name:   types.StringValue("people"),
			Source: types.StringValue("./data/people.json"),
			Format: types.StringValue("json"),
			Args:   []string{},
			Env:    map[string]string{},
			Config: map[string]string{},
		},
	}
	if diff := cmp.Diff(wantImports, model.Imports); diff != "" {
		t.Errorf("imports mismatch (-want +got):\n%s", diff)
	}
}

func TestPolicySetDataSourceReadErrors(t *testing.T) {
	emptyDir := t.TempDir()

	brokenDir := t.TempDir()
	writeTestFile(t, filepath.Join(brokenDir, "sentinel.hcl"), `policy "a" {`)

	missingPolicyDir := t.TempDir()
	writeTestFile(t, filepath.Join(missingPolicyDir, "sentinel.hcl"), `policy "a" {
  source = "./nowhere.sentinel"
}
`)

	tests := map[string]struct {
		path    string
		wantErr string
	}{
		"path does not exist": {
			path:    filepath.Join(emptyDir, "nope"),
			wantErr: "cannot read",
		},
		"directory without a config file": {
			path:    emptyDir,
			wantErr: "does not contain a sentinel.hcl file",
		},
		"unparseable config": {
			path:    brokenDir,
			wantErr: "failed to parse",
		},
		"policy source pointing nowhere": {
			path:    missingPolicyDir,
			wantErr: "cannot read",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, diags := readPolicySet(t, test.path)
			if !diags.HasError() {
				t.Fatal("Read succeeded, want an error")
			}
			if !strings.Contains(diags.Errors()[0].Detail(), test.wantErr) {
				t.Errorf("error = %q, want it to contain %q", diags.Errors()[0].Detail(), test.wantErr)
			}
		})
	}
}

func TestResolveConfigPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sentinel.hcl")
	writeTestFile(t, configPath, "")

	t.Run("a directory resolves to the config file it holds", func(t *testing.T) {
		got, err := resolveConfigPath(dir)
		if err != nil {
			t.Fatalf("resolveConfigPath: %s", err)
		}
		if got != configPath {
			t.Errorf("got %q, want %q", got, configPath)
		}
	})

	t.Run("a file is used as is", func(t *testing.T) {
		got, err := resolveConfigPath(configPath)
		if err != nil {
			t.Fatalf("resolveConfigPath: %s", err)
		}
		if got != configPath {
			t.Errorf("got %q, want %q", got, configPath)
		}
	})

	t.Run("an empty path is an error", func(t *testing.T) {
		if _, err := resolveConfigPath(""); err == nil {
			t.Fatal("resolveConfigPath succeeded, want an error")
		}
	})
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %s", path, err)
	}
	return string(content)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %s", path, err)
	}
}
