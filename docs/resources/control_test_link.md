# deixic_control_test_link

Creates a native control test and links it to a control by key. The test applies to every repository record. Every attribute forces replacement. Destroy unlinks the control and deletes the test record.

- `control_key`, `test_id`, `connection_id`, `name` (Required)
- `source` (default `github`), `aggregation` (default `all`), `control_test_type_id`, `repository_type_id` (Optional; types resolve from the installed library)
- `id`, `control_id` (Computed)
