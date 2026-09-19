---
page_title: "deixic_business_object Resource"
description: |-
  Manage a durable Deixic business object.
---

# deixic_business_object

Creates and manages one durable business object in the provider's configured
organization and workspace. The referenced type and schema revision must
already exist.

```terraform
resource "deixic_business_object" "customer" {
  type_id         = "customer"
  schema_revision = 3

  values = {
    name = {
      text = "Acme Corp"
    }
    seats = {
      integer = 25
    }
    active = {
      boolean = true
    }
    budget = {
      money_amount   = "1200.00"
      money_currency = "USD"
    }
    tags = {
      text_list = ["managed", "terraform"]
    }
  }
}
```

## Schema

### Required

- `type_id` (String, Forces replacement) Existing business object type ID.
- `schema_revision` (Number) Exact published schema revision. An update may
  move the revision forward; the owner rejects a move backward.
- `values` (Map of Object) Values keyed by the type's field IDs. Each object
  must set exactly one of:
  - `text`
  - `integer`
  - `boolean`
  - `decimal`, as exact canonical decimal text
  - both `money_amount` and `money_currency`
  - `enum_value`
  - `date`
  - `timestamp`
  - `reference_object_id`
  - `artifact_version_id`
  - `text_list`

The referenced type owns required-field, kind, enum, uniqueness, reference,
date, timestamp, decimal, money, and maximum-length validation. API validation
errors are returned directly to Terraform.

### Read-only

- `id` Server-assigned stable object ID.
- `api_endpoint` Normalized Platform API endpoint that owns this state.
- `organization_id` Organization scope that owns this state.
- `workspace_id` Workspace scope that owns this state.
- `revision` Owner-assigned optimistic concurrency revision.
- `created_at` Owner-recorded creation timestamp.
- `updated_at` Owner-recorded update timestamp.

The `values` map is stored in Terraform state. Do not use business object
fields for secrets. Use Deixic's secret-management path for secret material.

## Import

Import IDs include the organization, workspace, and object ID as independently
base64url-encoded components:

```text
v1.<base64url-organization>.<base64url-workspace>.<base64url-object>
```

The decoded organization and workspace must match the configured provider.
This prevents an import from silently reading the same object ID in a different
tenant scope.

For example, construct the identifier and import it with:

```sh
IMPORT_ID="$(printf '%s' "$ORG_ID" | base64 | tr '+/' '-_' | tr -d '=\n').$(printf '%s' "$WORKSPACE_ID" | base64 | tr '+/' '-_' | tr -d '=\n').$(printf '%s' "$OBJECT_ID" | base64 | tr '+/' '-_' | tr -d '=\n')"
terraform import deixic_business_object.customer "v1.${IMPORT_ID}"
```

## Deletion and drift

The Deixic owner records deletion as a tombstone. Refresh removes a tombstoned
object from Terraform state. A true API `not_found` response has the same
Terraform result.

Deletion can fail when the object is source-bound, participates in a durable
process, has a live relationship, or is the target of another object's
reference. Remove those owner relationships through their owning workflows
before destroying this resource.

Updates and deletes send the last read owner revision. A concurrent owner
change returns an `aborted` conflict; run another plan to refresh before
retrying.

The provider binds state to the endpoint, organization, and workspace that
created or imported it. Changing any of those provider settings causes refresh,
update, and delete to fail before an API request is sent. Keep the original
provider configuration to manage or remove that state, or use an explicit
Terraform state migration.

Each mutation uses one idempotency key and is not retried by the provider. If a
create response is lost after owner acceptance, locate the accepted object in
Deixic and import it before applying again; a second apply cannot safely infer
the server-assigned object ID.
