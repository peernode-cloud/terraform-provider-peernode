# Terraform provider for PeerNode

Manage [PeerNode](https://peernode.cloud) Kubernetes clusters with Terraform.

```hcl
terraform {
  required_providers {
    peernode = { source = "peernode-cloud/peernode" }
  }
}

provider "peernode" {} # token from PEERNODE_TOKEN (Settings → API tokens)

resource "peernode_kubernetes_cluster" "web" {
  name = "web" # autopilot: pay for what the pods request
}

resource "peernode_kubernetes_kubeconfig" "web" {
  cluster_id = peernode_kubernetes_cluster.web.id
}
```

| | |
| --- | --- |
| `peernode_kubernetes_cluster` (resource, data source) | A cluster in `autopilot` (default), `auto` or `pools` mode. Sizes change in place; `name` and `mode` force a new cluster. Import by ID. |
| `peernode_kubernetes_kubeconfig` (resource) | An admin kubeconfig. Issuing one revokes the previous token; change `keepers` to rotate. |

Full documentation: [docs/](docs/) (also on the Terraform Registry). The provider uses only the public
[PeerNode API](https://peernode.cloud) with an API token.

## Development

```sh
go test ./...          # runs Terraform against a fake API (needs terraform in PATH)
go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@latest generate --provider-name peernode
```

Try a local build with a `dev_overrides` block for `peernode-cloud/peernode` in `~/.terraformrc`.
Releases: push a `v*` tag; GitHub Actions builds and signs them with GoReleaser.

## License

[MPL-2.0](LICENSE)
