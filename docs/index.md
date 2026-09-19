---
page_title: "Deixic Provider"
description: |-
  Manage durable, tenant-scoped Deixic resources.
---

# Deixic Provider

The Deixic provider uses the Platform API's binary Connect contract to manage
resources in one organization and workspace.

## Example usage

```terraform
provider "deixic" {
  endpoint        = var.deixic_endpoint
  token           = var.deixic_token
  organization_id = var.deixic_organization_id
  workspace_id    = var.deixic_workspace_id
}
```

## Schema

### Optional

- `endpoint` (String) Base URL of the Deixic Platform API. May also be set with
  `DEIXIC_ENDPOINT`. HTTPS is required except for loopback development servers.
- `token` (String, Sensitive) Bearer token with `console:read` and
  `console:write`. May also be set with `DEIXIC_TOKEN`.
- `organization_id` (String) Organization scope. May also be set with
  `DEIXIC_ORGANIZATION_ID`.
- `workspace_id` (String) Workspace scope. May also be set with
  `DEIXIC_WORKSPACE_ID`.

Every value is required after environment-variable fallback is applied.
