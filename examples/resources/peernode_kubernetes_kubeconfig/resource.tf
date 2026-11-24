resource "peernode_kubernetes_kubeconfig" "web" {
  cluster_id = peernode_kubernetes_cluster.web.id
  # Change to rotate the token (the previous kubeconfig stops working).
  keepers = { rotation = "2026-11" }
}

provider "kubernetes" {
  config_path = local_sensitive_file.kubeconfig.filename
}

resource "local_sensitive_file" "kubeconfig" {
  content  = peernode_kubernetes_kubeconfig.web.kubeconfig
  filename = "${path.module}/web.kubeconfig"
}
