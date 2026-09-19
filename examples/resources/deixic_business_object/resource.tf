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
    budget = {
      money_amount   = "1200.00"
      money_currency = "USD"
    }
    tags = {
      text_list = ["managed", "terraform"]
    }
  }
}
