terraform {
  required_providers {
    peernode = {
      source = "peernode-cloud/peernode"
    }
  }
}

# The token can also come from PEERNODE_TOKEN.
provider "peernode" {
  token = var.peernode_token
}

variable "peernode_token" {
  type      = string
  sensitive = true
}
