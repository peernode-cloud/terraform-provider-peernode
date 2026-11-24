data "peernode_kubernetes_cluster" "web" {
  name = "web"
}

output "web_hosts" {
  value = data.peernode_kubernetes_cluster.web.public_hosts
}
