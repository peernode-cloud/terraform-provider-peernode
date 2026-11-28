# A private S3-compatible bucket (the default): use it with aws cli, rclone, restic or any S3 SDK.
resource "peernode_storage_bucket" "backups" {
  name = "acme-backups"
}

# A public bucket: anyone can download objects at public_url + key.
resource "peernode_storage_bucket" "assets" {
  name          = "acme-assets"
  visibility    = "public"
  node_count    = 3
  force_destroy = true # delete the objects with the bucket
}

# An end-to-end encrypted vault: files are encrypted in the browser or the peernode CLI; no S3 access.
resource "peernode_storage_bucket" "vault" {
  name = "acme-vault"
  kind = "vault"
}

output "assets_url" {
  value = peernode_storage_bucket.assets.public_url
}
