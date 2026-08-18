package sentinel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseConfigFile(t *testing.T) {
	path := filepath.Join("testdata", "policy-set", "sentinel.hcl")

	config, err := ParseConfigFile(path)
	if err != nil {
		t.Fatalf("ParseConfigFile: %s", err)
	}

	if config.Path != path {
		t.Errorf("Path = %q, want %q", config.Path, path)
	}
	if want := filepath.Join("testdata", "policy-set"); config.Dir() != want {
		t.Errorf("Dir() = %q, want %q", config.Dir(), want)
	}

	wantPolicies := []Policy{
		{
			Name:             "remote-policy",
			Source:           "git::https://github.com/hashicorp/sentinel-example.git//main.sentinel",
			EnforcementLevel: "soft-mandatory",
		},
		{
			Name:             "require-tags",
			Source:           "./require-tags.sentinel",
			EnforcementLevel: DefaultEnforcementLevel,
		},
		{
			Name:             "restrict-instance-type",
			Source:           "./restrict-instance-type.sentinel",
			EnforcementLevel: "hard-mandatory",
			Params: map[string]string{
				// Non-string params are JSON encoded so the map stays a
				// map(string).
				"allowed": `["t3.micro","t3.small"]`,
				"max":     "2",
				"label":   "production",
			},
		},
	}
	if diff := cmp.Diff(wantPolicies, config.Policies); diff != "" {
		t.Errorf("Policies mismatch (-want +got):\n%s", diff)
	}

	// Both `import "module"` and the older bare `module` block land in Modules,
	// since either way the config is naming a module.
	wantModules := []Module{
		{Name: "legacy-helpers", Source: "./modules/legacy-helpers.sentinel", Legacy: true},
		{Name: "tfplan-functions", Source: "./modules/tfplan-functions.sentinel"},
	}
	if diff := cmp.Diff(wantModules, config.Modules); diff != "" {
		t.Errorf("Modules mismatch (-want +got):\n%s", diff)
	}

	// Imports hold everything that is not a module.
	wantImports := []Import{
		{
			Type:   "plugin",
			Name:   "flipper",
			Source: "./plugins/flipper",
			Args:   []string{"-verbose"},
			Env:    map[string]string{"FLIPPER_LOG": "debug"},
		},
		// A standard import needs no source.
		{Type: "plugin", Name: "time", Config: map[string]string{"timezone": "Europe/Paris"}},
		{Type: "static", Name: "people", Source: "./data/people.json", Format: "json"},
	}
	if diff := cmp.Diff(wantImports, config.Imports); diff != "" {
		t.Errorf("Imports mismatch (-want +got):\n%s", diff)
	}
}

func TestParseConfig(t *testing.T) {
	tests := map[string]struct {
		src    string
		verify func(t *testing.T, config *Config)
	}{
		"empty config": {
			src: "",
			verify: func(t *testing.T, config *Config) {
				if len(config.Policies) != 0 {
					t.Errorf("got %d policies, want 0", len(config.Policies))
				}
			},
		},
		"policies are sorted by name": {
			src: `
policy "zebra" { source = "./z.sentinel" }
policy "alpha" { source = "./a.sentinel" }
policy "mike"  { source = "./m.sentinel" }
`,
			verify: func(t *testing.T, config *Config) {
				var names []string
				for _, policy := range config.Policies {
					names = append(names, policy.Name)
				}
				want := []string{"alpha", "mike", "zebra"}
				if diff := cmp.Diff(want, names); diff != "" {
					t.Errorf("names mismatch (-want +got):\n%s", diff)
				}
			},
		},
		"every enforcement level is accepted": {
			src: `
policy "a" {
  source            = "./a.sentinel"
  enforcement_level = "advisory"
}
policy "b" {
  source            = "./b.sentinel"
  enforcement_level = "soft-mandatory"
}
policy "c" {
  source            = "./c.sentinel"
  enforcement_level = "hard-mandatory"
}
`,
			verify: func(t *testing.T, config *Config) {
				want := []string{"advisory", "soft-mandatory", "hard-mandatory"}
				var got []string
				for _, policy := range config.Policies {
					got = append(got, policy.EnforcementLevel)
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("levels mismatch (-want +got):\n%s", diff)
				}
			},
		},
		"unknown blocks and attributes are ignored": {
			src: `
mock "tfplan/v2" {
  module {
    source = "./mock.sentinel"
  }
}

policy "a" {
  source        = "./a.sentinel"
  something_new = true
}
`,
			verify: func(t *testing.T, config *Config) {
				if len(config.Policies) != 1 {
					t.Fatalf("got %d policies, want 1", len(config.Policies))
				}
				if config.Policies[0].Source != "./a.sentinel" {
					t.Errorf("Source = %q", config.Policies[0].Source)
				}
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			config, err := ParseConfig([]byte(test.src), "sentinel.hcl")
			if err != nil {
				t.Fatalf("ParseConfig: %s", err)
			}
			test.verify(t, config)
		})
	}
}

func TestParseConfigJSON(t *testing.T) {
	src := `{
  "policy": {
    "restrict-instance-type": {
      "source": "./restrict-instance-type.sentinel",
      "enforcement_level": "hard-mandatory"
    }
  }
}`

	config, err := ParseConfig([]byte(src), "sentinel.json")
	if err != nil {
		t.Fatalf("ParseConfig: %s", err)
	}

	want := []Policy{{
		Name:             "restrict-instance-type",
		Source:           "./restrict-instance-type.sentinel",
		EnforcementLevel: "hard-mandatory",
	}}
	if diff := cmp.Diff(want, config.Policies); diff != "" {
		t.Errorf("Policies mismatch (-want +got):\n%s", diff)
	}
}

func TestParseConfigErrors(t *testing.T) {
	tests := map[string]struct {
		src     string
		wantErr string
	}{
		"syntax error": {
			src:     `policy "a" {`,
			wantErr: "failed to parse",
		},
		"policy without source": {
			src:     `policy "a" { enforcement_level = "advisory" }`,
			wantErr: `failed to read policy "a"`,
		},
		"module without source": {
			src:     `module "a" { }`,
			wantErr: `failed to read module "a"`,
		},
		"unknown enforcement level": {
			src: `
policy "a" {
  source            = "./a.sentinel"
  enforcement_level = "mandatory"
}
`,
			wantErr: `invalid enforcement_level "mandatory"`,
		},
		"non string source": {
			src:     `policy "a" { source = 42 }`,
			wantErr: `"source" must be a string`,
		},
		"non object params": {
			src: `
policy "a" {
  source = "./a.sentinel"
  params = "nope"
}
`,
			wantErr: "must be an object",
		},
		"duplicate policy": {
			src: `
policy "a" { source = "./a.sentinel" }
policy "a" { source = "./b.sentinel" }
`,
			wantErr: `policy "a" is declared more than once`,
		},
		"duplicate module": {
			src: `
module "a" { source = "./a.sentinel" }
module "a" { source = "./b.sentinel" }
`,
			wantErr: `module "a" is declared more than once`,
		},
		"duplicate import": {
			src: `
import "plugin" "a" { source = "./a" }
import "plugin" "a" { source = "./b" }
`,
			wantErr: `import "plugin" "a" is declared more than once`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseConfig([]byte(test.src), "sentinel.hcl")
			if err == nil {
				t.Fatal("ParseConfig succeeded, want an error")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

// TestParseExamplePolicySet keeps the policy set shipped under examples/ honest:
// it must parse, and every source it names must exist on disk, since the local
// example is meant to run as it is.
func TestParseExamplePolicySet(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "policies", "sentinel.hcl")

	config, err := ParseConfigFile(path)
	if err != nil {
		t.Fatalf("ParseConfigFile: %s", err)
	}

	wantLevels := map[string]string{
		"restrict-instance-type": "hard-mandatory",
		"require-tags":           "soft-mandatory",
	}
	if len(config.Policies) != len(wantLevels) {
		t.Errorf("got %d policies, want %d", len(config.Policies), len(wantLevels))
	}

	for _, policy := range config.Policies {
		want, ok := wantLevels[policy.Name]
		if !ok {
			t.Errorf("unexpected policy %q", policy.Name)
			continue
		}
		if policy.EnforcementLevel != want {
			t.Errorf("policy %q: enforcement_level = %q, want %q", policy.Name, policy.EnforcementLevel, want)
		}

		source, local := config.ResolvePath(policy.Source)
		if !local {
			t.Errorf("policy %q: source %q is not a local path", policy.Name, policy.Source)
			continue
		}
		if _, err := os.Stat(source); err != nil {
			t.Errorf("policy %q: %s", policy.Name, err)
		}

		// Each policy needs a test directory, or `sentinel test` has nothing to
		// run and the example proves nothing.
		testDir := filepath.Join(config.Dir(), "test", policy.Name)
		entries, err := os.ReadDir(testDir)
		if err != nil {
			t.Errorf("policy %q: %s", policy.Name, err)
			continue
		}

		var cases int
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".hcl") {
				cases++
			}
		}
		if cases == 0 {
			t.Errorf("policy %q: %s holds no test case", policy.Name, testDir)
		}
	}
}

func TestParseConfigFileMissing(t *testing.T) {
	_, err := ParseConfigFile(filepath.Join("testdata", "does-not-exist.hcl"))
	if err == nil {
		t.Fatal("ParseConfigFile succeeded, want an error")
	}
}

func TestConfigResolvePath(t *testing.T) {
	config := &Config{Path: filepath.Join("policies", "sentinel.hcl")}

	tests := map[string]struct {
		source   string
		wantPath string
		wantOK   bool
	}{
		"relative source": {
			source:   "./restrict.sentinel",
			wantPath: filepath.Join("policies", "restrict.sentinel"),
			wantOK:   true,
		},
		"source in a subdirectory": {
			source:   "modules/helpers.sentinel",
			wantPath: filepath.Join("policies", "modules", "helpers.sentinel"),
			wantOK:   true,
		},
		"parent directory source": {
			source:   "../common/helpers.sentinel",
			wantPath: filepath.Join("common", "helpers.sentinel"),
			wantOK:   true,
		},
		"absolute source": {
			source:   filepath.Join(string(filepath.Separator), "opt", "policies", "a.sentinel"),
			wantPath: filepath.Join(string(filepath.Separator), "opt", "policies", "a.sentinel"),
			wantOK:   true,
		},
		"https source is not local": {
			source:   "https://example.com/a.sentinel",
			wantPath: "https://example.com/a.sentinel",
			wantOK:   false,
		},
		"git source is not local": {
			source:   "git::https://example.com/policies.git",
			wantPath: "git::https://example.com/policies.git",
			wantOK:   false,
		},
		"mercurial source is not local": {
			source:   "hg::https://example.com/policies",
			wantPath: "hg::https://example.com/policies",
			wantOK:   false,
		},
		"gcp source is not local": {
			source:   "gcp::https://www.googleapis.com/storage/v1/policies/a.sentinel",
			wantPath: "gcp::https://www.googleapis.com/storage/v1/policies/a.sentinel",
			wantOK:   false,
		},
		"bare github source is not local": {
			source:   "github.com/hashicorp/sentinel-example",
			wantPath: "github.com/hashicorp/sentinel-example",
			wantOK:   false,
		},
		// file:: forces the local filesystem, so the prefix is not part of the
		// path on disk.
		"file protocol is local": {
			source:   "file::./restrict.sentinel",
			wantPath: filepath.Join("policies", "restrict.sentinel"),
			wantOK:   true,
		},
		// A checksum query is a Sentinel feature, not part of the path.
		"local source with a checksum query": {
			source:   "./restrict.sentinel?checksum=md5:b7d96c89d09d9e204f5fedc4d5d55b21",
			wantPath: filepath.Join("policies", "restrict.sentinel"),
			wantOK:   true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			path, ok := config.ResolvePath(test.source)
			if path != test.wantPath {
				t.Errorf("path = %q, want %q", path, test.wantPath)
			}
			if ok != test.wantOK {
				t.Errorf("ok = %t, want %t", ok, test.wantOK)
			}
		})
	}
}
