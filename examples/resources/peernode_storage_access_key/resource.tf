resource "peernode_storage_bucket" "assets" {
  name = "acme-assets"
}

# A key limited to one bucket. Change keepers to rotate it.
resource "peernode_storage_access_key" "ci" {
  name    = "ci deploy"
  bucket  = peernode_storage_bucket.assets.name
  keepers = { rotated = "2026-12" }
}

# The official AWS provider manages objects through the PeerNode S3 endpoint. Terraform cannot configure a
# provider from resources that do not exist yet, so create the key first:
#   terraform apply -target=peernode_storage_access_key.ci && terraform apply
# (or keep the bucket and key in a separate configuration and pass the key in as variables).
provider "aws" {
  alias                       = "peernode"
  region                      = peernode_storage_access_key.ci.region
  access_key                  = peernode_storage_access_key.ci.access_key_id
  secret_key                  = peernode_storage_access_key.ci.secret_access_key
  s3_use_path_style           = true
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
  skip_region_validation      = true
  endpoints {
    s3 = peernode_storage_access_key.ci.endpoint
  }
}

resource "aws_s3_object" "index" {
  provider     = aws.peernode
  bucket       = peernode_storage_bucket.assets.name
  key          = "index.html"
  content      = "<h1>Hello</h1>"
  content_type = "text/html"
}
