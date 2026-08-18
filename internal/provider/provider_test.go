package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// TestProviderSchema asks the provider server for its whole schema. This is the
// same call Terraform makes first, and it validates every data source schema and
// function definition, so a malformed schema fails here rather than at plan time.
func TestProviderSchema(t *testing.T) {
	ctx := context.Background()

	server := providerserver.NewProtocol6(New("test")())()
	resp, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema: %s", err)
	}

	for _, diag := range resp.Diagnostics {
		if diag.Severity == tfprotov6.DiagnosticSeverityError {
			t.Errorf("schema error: %s: %s", diag.Summary, diag.Detail)
		}
	}

	if _, ok := resp.DataSourceSchemas["sentinel_policy_set"]; !ok {
		t.Errorf("data source sentinel_policy_set is missing, got %v", keys(resp.DataSourceSchemas))
	}

	for _, name := range []string{"test", "test_report"} {
		if _, ok := resp.Functions[name]; !ok {
			t.Errorf("function %q is missing, got %v", name, keys(resp.Functions))
		}
	}

	if len(resp.ResourceSchemas) != 0 {
		t.Errorf("the provider declares resources, want none: %v", keys(resp.ResourceSchemas))
	}
}

func TestProviderMetadata(t *testing.T) {
	ctx := context.Background()

	server := providerserver.NewProtocol6(New("1.2.3")())()
	resp, err := server.GetMetadata(ctx, &tfprotov6.GetMetadataRequest{})
	if err != nil {
		t.Fatalf("GetMetadata: %s", err)
	}

	if len(resp.DataSources) != 1 {
		t.Errorf("got %d data sources, want 1", len(resp.DataSources))
	}
	if len(resp.Functions) != 2 {
		t.Errorf("got %d functions, want 2", len(resp.Functions))
	}
}

func keys[V any](m map[string]V) []string {
	result := make([]string, 0, len(m))
	for key := range m {
		result = append(result, key)
	}
	return result
}
