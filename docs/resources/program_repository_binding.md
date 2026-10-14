# deixic_program_repository_binding

Binds one GitHub repository to a program: creates the repository record, binds it to the connection's observed GitHub resource family (`family_stable_id`, default classic branch protection), and links it to the program through the `repositories` relationship. Every attribute forces replacement. Destroy unlinks the repository from the program and keeps the source-bound record.

- `program_id`, `connection_id`, `repository` (full name), `github_repo_id` (Required)
- `family_stable_id`, `record_suffix`, `repository_type_id` (Optional) Defaults: `resource_family:github.classic_protection.observed`, `classic_protection`, resolved from the library.
