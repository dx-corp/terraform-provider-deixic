resource "deixic_program" "soc2" {
  name          = "dx-corp SOC 2"
  goal          = "SOC 2 Type 1 readiness"
  owner_label   = "Jonathan Haas"
  framework_ids = [for f in data.deixic_frameworks.soc2.records : f.id]
}
