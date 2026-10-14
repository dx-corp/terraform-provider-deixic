# deixic_frameworks / deixic_controls / deixic_business_blueprints

- `deixic_frameworks` and `deixic_controls` list installed framework and control records as `records` (`id`, `key`, `name`). Filter with `key` (exact) or `search` (case-insensitive substring of key or name). `type_id` is resolved from the installed library when omitted.
- `deixic_business_blueprints` lists catalog blueprints as `blueprints` (`blueprint_id`, `version`, `content_digest`, `name`, `description`); filter with `id_prefix`.

```terraform
data "deixic_frameworks" "soc2" { search = "soc" }
data "deixic_controls" "chg03" { key = "DX-CHG-03" }
data "deixic_business_blueprints" "soc2" { id_prefix = "cerebro-framework-soc2" }
```
