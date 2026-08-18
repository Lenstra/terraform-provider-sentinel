// Package provider implements the Terraform provider for Sentinel.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Ensure SentinelProvider satisfies various provider interfaces.
var _ provider.Provider = &SentinelProvider{}
var _ provider.ProviderWithFunctions = &SentinelProvider{}

// SentinelProvider defines the provider implementation.
type SentinelProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

func (p *SentinelProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "sentinel"
	resp.Version = p.version
}

// Schema is empty on purpose. The only thing the provider would want to
// configure is the location of the Sentinel CLI, but provider defined functions
// cannot read the provider configuration, so the CLI is located through the
// SENTINEL_BINARY environment variable instead. Keeping the schema empty avoids
// a setting that would silently apply to the data source only.
func (p *SentinelProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads Sentinel policy configuration and runs Sentinel tests from Terraform.",
	}
}

func (p *SentinelProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
}

func (p *SentinelProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{}
}

func (p *SentinelProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewPolicySetDataSource,
	}
}

func (p *SentinelProvider) Functions(ctx context.Context) []func() function.Function {
	return []func() function.Function{
		NewTestFunction,
		NewTestReportFunction,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &SentinelProvider{
			version: version,
		}
	}
}
