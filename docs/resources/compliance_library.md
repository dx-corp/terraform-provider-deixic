# deixic_compliance_library

The workspace's shared compliance library. Apply previews the reconcile plan, applies it against the previewed plan digest, and polls the server job until it commits. The plan digest is stored in state.

`blueprint_ids` is the complete set of framework packs. The newest catalog version of each is installed, and packs left out are withdrawn. Destroying the resource removes it from state and leaves the installed library in place.

## Example

```terraform
resource "deixic_compliance_library" "library" {
  blueprint_ids = ["cerebro-framework-soc2"]
}
```

## Attributes

- `blueprint_ids` (Required, Set of String) Framework pack blueprint IDs.
- `library_type_id` (Optional) Type ID of the `control_test` type that owns the library. Resolved when omitted.
- `id`, `selections`, `plan_digest`, `applied_changes`, `pending_changes` (Computed) Library clone ID, applied pack versions and digests, applied plan digest, and change counts. A non-zero `pending_changes` after refresh is drift and plans an update.
