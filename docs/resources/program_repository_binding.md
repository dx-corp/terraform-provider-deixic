# deixic_program_repository_binding

Creates a repository record, submits a governed source-binding intent, and links the record to an existing program through its `repositories` relationship. Destroy removes the relationship and retains the source-bound record. Every configured attribute requires replacement.

Required configuration:

- `program_id`: an authorized program whose pinned schema declares `repositories` targeting `repository_type_id`. A program created by the default `deixic_program` schema has only `frameworks`; that alone does not establish repository-link support.
- `connection_id`, `repository` (full name), and `github_repo_id`.
- `repository_type_id`: an existing published type with a text display field and compatible source groups. Creation uses its published revision and display field.
- `source_group_ids`: between 1 and 64 explicit group IDs from that type.
- `source_fields`: a map from every field in the selected groups to `display_name` or `external_id`. These are references to properties resolved by the source owner, not caller-supplied observed values. Every selected field must support source mapping, and mappings outside the selected groups are rejected.

`ListBusinessObjectTypes` returns the published revision, display field, and field/group IDs with `source_mappable` metadata. The provider validates this metadata before creating a record; the source owner performs admission against the actual authorized observation.

The built-in repository `details` group includes the `asset` reference field, so that group cannot be source-bound. Use a compatible existing group or publish an additive schema revision with optional text fields in a new group through the type owner. Existing field groups and relationship targets remain immutable. The selected program record must also use a schema revision with the required repository relationship.

Optional `family_stable_id` and `record_suffix` default to `resource_family:github.classic_protection.observed` and `classic_protection`. The source record ID is `github_repo_id` followed by `record_suffix`.

Creation and binding keys include the canonical type/revision, initial repository value, source coordinates, groups, and field mappings. Retrying the same intent reuses its record and reads current owner state, and replays the exact binding command through its matching owner receipt before linking. A foreign binding without that receipt is refused. A changed intent uses a different creation key; it cannot silently adopt a previously bound record. Records retained from earlier intents remain source-owned.
