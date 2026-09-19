package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestProviderTokenSchemaIsSensitive(t *testing.T) {
	implementation := &deixicProvider{version: "test"}
	response := &frameworkprovider.SchemaResponse{}
	implementation.Schema(context.Background(), frameworkprovider.SchemaRequest{}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	token, ok := response.Schema.Attributes["token"].(providerschema.StringAttribute)
	if !ok {
		t.Fatalf("token has unexpected schema type %T", response.Schema.Attributes["token"])
	}
	if !token.Sensitive {
		t.Fatal("provider token must be marked sensitive")
	}
}

func TestConfiguredStringRejectsUnknownWithoutEnvironmentFallback(t *testing.T) {
	t.Setenv("DEIXIC_TEST_VALUE", "ambient-value")
	var diagnostics diag.Diagnostics
	value := configuredString(types.StringUnknown(), "test_value", "DEIXIC_TEST_VALUE", &diagnostics)
	if value != "" {
		t.Fatalf("unknown configuration resolved to %q", value)
	}
	if !diagnostics.HasError() {
		t.Fatal("unknown configuration must produce an error")
	}
}

func TestConfiguredStringUsesEnvironmentOnlyForNull(t *testing.T) {
	t.Setenv("DEIXIC_TEST_VALUE", "ambient-value")
	var diagnostics diag.Diagnostics
	if got := configuredString(types.StringNull(), "test_value", "DEIXIC_TEST_VALUE", &diagnostics); got != "ambient-value" {
		t.Fatalf("null configuration resolved to %q", got)
	}
	if got := configuredString(types.StringValue(""), "test_value", "DEIXIC_TEST_VALUE", &diagnostics); got != "" {
		t.Fatalf("explicit empty configuration resolved to %q", got)
	}
}
