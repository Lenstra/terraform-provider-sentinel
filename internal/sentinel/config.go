// Package sentinel reads Sentinel CLI configuration files and drives the
// Sentinel CLI itself. It deliberately knows nothing about Terraform so that it
// can be unit tested on its own.
package sentinel

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// ConfigFileName is the name Sentinel gives its configuration file.
const ConfigFileName = "sentinel.hcl"

// DefaultEnforcementLevel is the level Sentinel assumes for a policy that does
// not set one explicitly.
const DefaultEnforcementLevel = "advisory"

// EnforcementLevels lists the levels a policy may declare, weakest first.
var EnforcementLevels = []string{"advisory", "soft-mandatory", "hard-mandatory"}

// Import types, the first label of an import block.
const (
	// ImportTypeModule maps an import onto a Sentinel module.
	ImportTypeModule = "module"
	// ImportTypePlugin configures a standard import or an import plugin.
	ImportTypePlugin = "plugin"
	// ImportTypeStatic exposes a data file as an import.
	ImportTypeStatic = "static"
)

// Config is a parsed sentinel.hcl file.
type Config struct {
	// Path is the sentinel.hcl file the config was read from.
	Path string

	Policies []Policy
	Modules  []Module
	Imports  []Import
}

// Policy is a `policy "name" { ... }` block.
type Policy struct {
	Name string
	// Source is the source as written in the configuration.
	Source string
	// EnforcementLevel is the declared level, or DefaultEnforcementLevel when
	// the block omits it.
	EnforcementLevel string
	// Params holds the block's `params` attribute. Values that are not strings
	// are JSON encoded so that the whole map stays a map of strings.
	Params map[string]string
}

// Module is a Sentinel module made available to the policies.
//
// Current Sentinel declares one as `import "module" "name" { source = ... }`.
// Older configurations use a bare `module "name" { ... }` block, which is still
// found in the wild, so both forms are read and reported here.
type Module struct {
	Name   string
	Source string
	// Legacy marks a module that came from a bare `module` block rather than
	// from `import "module"`.
	Legacy bool
}

// Import is an `import "type" "name" { ... }` block other than
// `import "module"`, which is reported as a Module instead.
//
// Not every attribute applies to every type: Format belongs to a static import,
// while Args, Env and Config belong to a plugin.
type Import struct {
	Type   string
	Name   string
	Source string
	Format string
	Args   []string
	Env    map[string]string
	Config map[string]string
}

// configSchema only claims the blocks we understand. A sentinel.hcl may also
// hold mock, global, param, test and sentinel blocks, which configure a CLI run
// rather than the policy set, so they are ignored instead of rejected.
var configSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "policy", LabelNames: []string{"name"}},
		{Type: "module", LabelNames: []string{"name"}},
		{Type: "import", LabelNames: []string{"type", "name"}},
	},
}

var policySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "source", Required: true},
		{Name: "enforcement_level"},
		{Name: "params"},
	},
}

var moduleSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "source", Required: true},
	},
}

var importSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "source"},
		{Name: "format"},
		{Name: "args"},
		{Name: "env"},
		{Name: "config"},
	},
}

// ParseConfigFile reads and parses the sentinel.hcl file at path.
func ParseConfigFile(path string) (*Config, error) {
	parser := hclparse.NewParser()

	var file *hcl.File
	var diags hcl.Diagnostics
	if strings.HasSuffix(path, ".json") {
		file, diags = parser.ParseJSONFile(path)
	} else {
		file, diags = parser.ParseHCLFile(path)
	}
	if diags.HasErrors() {
		return nil, fmt.Errorf("failed to parse %s: %w", path, diags)
	}

	return parseConfigBody(path, file.Body)
}

// ParseConfig parses src as a sentinel.hcl file. filename is only used to
// report diagnostics.
func ParseConfig(src []byte, filename string) (*Config, error) {
	parser := hclparse.NewParser()

	var file *hcl.File
	var diags hcl.Diagnostics
	if strings.HasSuffix(filename, ".json") {
		file, diags = parser.ParseJSON(src, filename)
	} else {
		file, diags = parser.ParseHCL(src, filename)
	}
	if diags.HasErrors() {
		return nil, fmt.Errorf("failed to parse %s: %w", filename, diags)
	}

	return parseConfigBody(filename, file.Body)
}

func parseConfigBody(path string, body hcl.Body) (*Config, error) {
	// PartialContent rather than Content: unknown top-level blocks and
	// attributes are not our business.
	content, _, diags := body.PartialContent(configSchema)
	if diags.HasErrors() {
		return nil, fmt.Errorf("failed to read %s: %w", path, diags)
	}

	config := &Config{Path: path}

	for _, block := range content.Blocks {
		switch block.Type {
		case "policy":
			policy, err := decodePolicy(block)
			if err != nil {
				return nil, err
			}
			config.Policies = append(config.Policies, *policy)
		case "module":
			module, err := decodeLegacyModule(block)
			if err != nil {
				return nil, err
			}
			config.Modules = append(config.Modules, *module)
		case "import":
			// `import "module"` names a module, so it is reported as one rather
			// than split across two outputs by the form it was written in.
			if block.Labels[0] == ImportTypeModule {
				module, err := decodeModuleImport(block)
				if err != nil {
					return nil, err
				}
				config.Modules = append(config.Modules, *module)
				continue
			}

			imp, err := decodeImport(block)
			if err != nil {
				return nil, err
			}
			config.Imports = append(config.Imports, *imp)
		}
	}

	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}

	sort.Slice(config.Policies, func(i, j int) bool { return config.Policies[i].Name < config.Policies[j].Name })
	sort.Slice(config.Modules, func(i, j int) bool { return config.Modules[i].Name < config.Modules[j].Name })
	sort.Slice(config.Imports, func(i, j int) bool {
		if config.Imports[i].Type != config.Imports[j].Type {
			return config.Imports[i].Type < config.Imports[j].Type
		}
		return config.Imports[i].Name < config.Imports[j].Name
	})

	return config, nil
}

func decodePolicy(block *hcl.Block) (*Policy, error) {
	content, _, diags := block.Body.PartialContent(policySchema)
	if diags.HasErrors() {
		return nil, fmt.Errorf("failed to read policy %q: %w", block.Labels[0], diags)
	}

	policy := &Policy{
		Name:             block.Labels[0],
		EnforcementLevel: DefaultEnforcementLevel,
	}

	var err error
	if policy.Source, err = requiredString(content.Attributes, "source"); err != nil {
		return nil, fmt.Errorf("policy %q: %w", policy.Name, err)
	}

	if attr, ok := content.Attributes["enforcement_level"]; ok {
		level, err := attrString(attr)
		if err != nil {
			return nil, fmt.Errorf("policy %q: %w", policy.Name, err)
		}
		if !isEnforcementLevel(level) {
			return nil, fmt.Errorf(
				"policy %q: invalid enforcement_level %q, must be one of %s",
				policy.Name, level, strings.Join(EnforcementLevels, ", "),
			)
		}
		policy.EnforcementLevel = level
	}

	if attr, ok := content.Attributes["params"]; ok {
		if policy.Params, err = attrStringMap(attr); err != nil {
			return nil, fmt.Errorf("policy %q: params: %w", policy.Name, err)
		}
	}

	return policy, nil
}

// decodeLegacyModule reads a bare `module "name" { ... }` block.
func decodeLegacyModule(block *hcl.Block) (*Module, error) {
	content, _, diags := block.Body.PartialContent(moduleSchema)
	if diags.HasErrors() {
		return nil, fmt.Errorf("failed to read module %q: %w", block.Labels[0], diags)
	}

	module := &Module{Name: block.Labels[0], Legacy: true}

	var err error
	if module.Source, err = requiredString(content.Attributes, "source"); err != nil {
		return nil, fmt.Errorf("module %q: %w", module.Name, err)
	}

	return module, nil
}

// decodeModuleImport reads an `import "module" "name" { ... }` block.
func decodeModuleImport(block *hcl.Block) (*Module, error) {
	content, _, diags := block.Body.PartialContent(importSchema)
	if diags.HasErrors() {
		return nil, fmt.Errorf("failed to read import %q %q: %w", block.Labels[0], block.Labels[1], diags)
	}

	module := &Module{Name: block.Labels[1]}

	source, err := requiredString(content.Attributes, "source")
	if err != nil {
		return nil, fmt.Errorf("import \"module\" %q: %w", module.Name, err)
	}
	module.Source = source

	return module, nil
}

func decodeImport(block *hcl.Block) (*Import, error) {
	content, _, diags := block.Body.PartialContent(importSchema)
	if diags.HasErrors() {
		return nil, fmt.Errorf("failed to read import %q %q: %w", block.Labels[0], block.Labels[1], diags)
	}

	imp := &Import{Type: block.Labels[0], Name: block.Labels[1]}
	describe := fmt.Sprintf("import %q %q", imp.Type, imp.Name)

	// A standard import needs no source: `import "plugin" "time"` names an
	// import the runtime already provides.
	for name, target := range map[string]*string{"source": &imp.Source, "format": &imp.Format} {
		attr, ok := content.Attributes[name]
		if !ok {
			continue
		}
		value, err := attrString(attr)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", describe, err)
		}
		*target = value
	}

	if attr, ok := content.Attributes["args"]; ok {
		args, err := attrStringList(attr)
		if err != nil {
			return nil, fmt.Errorf("%s: args: %w", describe, err)
		}
		imp.Args = args
	}

	for name, target := range map[string]*map[string]string{"env": &imp.Env, "config": &imp.Config} {
		attr, ok := content.Attributes[name]
		if !ok {
			continue
		}
		values, err := attrStringMap(attr)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", describe, name, err)
		}
		*target = values
	}

	return imp, nil
}

func (c *Config) validate() error {
	seen := map[string]struct{}{}
	for _, policy := range c.Policies {
		if _, ok := seen[policy.Name]; ok {
			return fmt.Errorf("policy %q is declared more than once", policy.Name)
		}
		seen[policy.Name] = struct{}{}
	}

	seen = map[string]struct{}{}
	for _, module := range c.Modules {
		if _, ok := seen[module.Name]; ok {
			return fmt.Errorf("module %q is declared more than once", module.Name)
		}
		seen[module.Name] = struct{}{}
	}

	seen = map[string]struct{}{}
	for _, imp := range c.Imports {
		key := imp.Type + "." + imp.Name
		if _, ok := seen[key]; ok {
			return fmt.Errorf("import %q %q is declared more than once", imp.Type, imp.Name)
		}
		seen[key] = struct{}{}
	}

	return nil
}

// Dir is the directory holding the configuration file, which is what Sentinel
// resolves relative sources against.
func (c *Config) Dir() string {
	return filepath.Dir(c.Path)
}

// ResolvePath turns a source as written in the configuration into a path
// relative to the configuration file. Sources that are not local paths are
// returned as-is with ok set to false, since there is nothing on disk to read.
func (c *Config) ResolvePath(source string) (path string, ok bool) {
	source, local := localSource(source)
	if !local {
		return source, false
	}
	if filepath.IsAbs(source) {
		return filepath.Clean(source), true
	}
	return filepath.Join(c.Dir(), source), true
}

// forcedRemoteProtocols are the Sentinel source prefixes that force a protocol
// other than the local filesystem. Sentinel also accepts `file::`, which forces
// the local filesystem and is handled separately.
var forcedRemoteProtocols = []string{"git::", "hg::", "http::", "https::", "s3::", "gcp::"}

// remoteSchemes are bare source prefixes Sentinel resolves over the network.
var remoteSchemes = []string{"http://", "https://", "github.com/", "bitbucket.org/"}

// localSource reports whether a source points at the local filesystem, and
// returns the path to use when it does.
//
// A local source may carry a `file::` prefix forcing the local filesystem, and
// any source may carry a `?checksum=` query, neither of which is part of the
// path on disk.
func localSource(source string) (path string, ok bool) {
	for _, prefix := range forcedRemoteProtocols {
		if strings.HasPrefix(source, prefix) {
			return source, false
		}
	}
	for _, scheme := range remoteSchemes {
		if strings.HasPrefix(source, scheme) {
			return source, false
		}
	}

	path = strings.TrimPrefix(source, "file::")
	if index := strings.Index(path, "?"); index >= 0 {
		path = path[:index]
	}
	if path == "" {
		return source, false
	}

	return path, true
}

func isEnforcementLevel(level string) bool {
	for _, candidate := range EnforcementLevels {
		if candidate == level {
			return true
		}
	}
	return false
}

func requiredString(attrs hcl.Attributes, name string) (string, error) {
	attr, ok := attrs[name]
	if !ok {
		return "", fmt.Errorf("missing required attribute %q", name)
	}
	return attrString(attr)
}

// attrString evaluates an attribute that must be a literal string. sentinel.hcl
// has no variables or functions, so an empty EvalContext is enough.
func attrString(attr *hcl.Attribute) (string, error) {
	value, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return "", fmt.Errorf("failed to evaluate %q: %w", attr.Name, diags)
	}
	if value.IsNull() {
		return "", fmt.Errorf("%q must not be null", attr.Name)
	}
	if value.Type() != cty.String {
		return "", fmt.Errorf("%q must be a string, got %s", attr.Name, value.Type().FriendlyName())
	}
	return value.AsString(), nil
}

// attrStringList evaluates an attribute that must be a list or tuple of strings.
func attrStringList(attr *hcl.Attribute) ([]string, error) {
	value, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return nil, fmt.Errorf("failed to evaluate %q: %w", attr.Name, diags)
	}
	if value.IsNull() {
		return nil, nil
	}
	if !value.Type().IsTupleType() && !value.Type().IsListType() && !value.Type().IsSetType() {
		return nil, fmt.Errorf("must be a list, got %s", value.Type().FriendlyName())
	}

	result := []string{}
	for _, item := range value.AsValueSlice() {
		if item.IsNull() {
			return nil, fmt.Errorf("must not contain null values")
		}
		if item.Type() != cty.String {
			return nil, fmt.Errorf("must be a list of strings, got a %s element", item.Type().FriendlyName())
		}
		result = append(result, item.AsString())
	}

	return result, nil
}

// attrStringMap evaluates an attribute that must be an object or a map. Values
// that are already strings are kept verbatim; anything else is JSON encoded so
// callers get a predictable map(string) without losing information.
func attrStringMap(attr *hcl.Attribute) (map[string]string, error) {
	value, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return nil, fmt.Errorf("failed to evaluate %q: %w", attr.Name, diags)
	}
	if value.IsNull() {
		return nil, nil
	}
	if !value.Type().IsObjectType() && !value.Type().IsMapType() {
		return nil, fmt.Errorf("must be an object, got %s", value.Type().FriendlyName())
	}

	result := map[string]string{}
	for key, item := range value.AsValueMap() {
		encoded, err := encodeValue(item)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", key, err)
		}
		result[key] = encoded
	}

	return result, nil
}

func encodeValue(value cty.Value) (string, error) {
	if value.IsNull() {
		return "", nil
	}
	if value.Type() == cty.String {
		return value.AsString(), nil
	}

	encoded, err := ctyjson.Marshal(value, value.Type())
	if err != nil {
		return "", fmt.Errorf("failed to encode value: %w", err)
	}
	return string(encoded), nil
}
