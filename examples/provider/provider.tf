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

variable "deixic_endpoint" {
  type = string
}

variable "deixic_token" {
  type      = string
  sensitive = true
}

variable "deixic_organization_id" {
  type = string
}

variable "deixic_workspace_id" {
  type = string
}
