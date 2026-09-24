# Terraform Provider for Deixic

The Deixic provider manages durable, tenant-scoped Deixic resources through the
Platform API. This initial provider deliberately exposes one resource whose
owner has a complete lifecycle: `deixic_business_object`.

The provider uses Terraform Plugin Protocol 6 and Deixic's canonical binary
protobuf contract. Calls go to
`/deixicpublic.v1.DeixicPublicService/{Create,Get,Update,Delete}BusinessObject` with
`Content-Type: application/proto`, `Connect-Protocol-Version: 1`, bearer
authentication, and explicit organization and workspace scope.

## Requirements

- Terraform 1.9 or later
- Go 1.26.6 or later to build the provider
- A Deixic Platform API endpoint
- A bearer token with `console:read` and `console:write`
- An existing published business object type and exact schema revision

## Configuration

```hcl
terraform {
  required_providers {
    deixic = {
      source = "dx-corp/deixic"
    }
  }
}

provider "deixic" {
  endpoint        = var.deixic_endpoint
  token           = var.deixic_token
  organization_id = var.deixic_organization_id
  workspace_id    = var.deixic_workspace_id
}
```

Each setting may instead come from `DEIXIC_ENDPOINT`, `DEIXIC_TOKEN`,
`DEIXIC_ORGANIZATION_ID`, or `DEIXIC_WORKSPACE_ID`. The token attribute is
marked sensitive and is used only to configure the API client. Terraform does
not store it in resource state. API endpoints must use HTTPS; plaintext HTTP is
accepted only for loopback development and contract tests.

See [the business object documentation](docs/resources/business_object.md) for
the complete resource shape, import format, and lifecycle limits.

## Development

The provider vendors only the generated `deixicpublic.v1` binding from Mono's
reviewed public proto source. The package audit parses its embedded descriptor
and the release gate also checks the compiled provider binary.

```sh
go test ./...
go build ./...
```

The acceptance suite runs real Terraform CLI plan, apply, refresh, import,
update, and destroy operations against an in-process HTTP server that enforces
the production binary protobuf, Connect, authentication, and tenant-scope
contract. It never calls production.

```sh
TF_ACC=1 TF_ACC_TERRAFORM_VERSION=1.16.3 \
  go test ./internal/provider -run '^TestAcc' -v -count=1
```

`terraform-plugin-testing` installs that pinned Terraform CLI release for the
test. The suite also passes with Terraform 1.9.8.

## Current scope

The provider does not manage business object types. Deixic type definitions
are immutable revisions and the owner does not expose deletion semantics, so
presenting them as an ordinary Terraform CRUD resource would make destroy
misleading. Create or publish the type through its existing owner workflow,
then pass its `type_id` and `schema_revision` to this provider.

Deixic rejects deletion of source-bound records, process participants, and
objects with live relationships or inbound references. The provider surfaces
those owner errors and does not detach or delete related state implicitly.

Each create, update, and delete invocation uses one owner idempotency key. The
provider does not retry mutations. If the connection is lost after the owner
accepted a create but before Terraform received its response, Terraform cannot
recover the server-assigned object ID automatically; reconcile that object and
import it before applying again.
