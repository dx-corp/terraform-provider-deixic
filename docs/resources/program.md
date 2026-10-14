# deixic_program

A compliance program such as "dx-corp SOC 2", and the frameworks it tracks.

`name` identifies the program: creating it again with the same name replays the original record. `framework_ids` links framework records through the program's `frameworks` relationship; changes add and remove links. Destroy removes the links and deletes the program record.

## Attributes

- `name` (Required), `goal`, `owner_label`, `due_date` (Optional).
- `framework_ids` (Optional, Set of String) Framework record IDs.
- `id`, `revision` (Computed).
