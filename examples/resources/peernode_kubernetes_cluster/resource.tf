# Autopilot (default): pay for what the pods request. The cap is optional.
resource "peernode_kubernetes_cluster" "web" {
  name = "web"
}

# Autopilot with a spending cap.
resource "peernode_kubernetes_cluster" "capped" {
  name      = "capped"
  autopilot = { max_cpu = 2, max_memory_gb = 4, max_disk_gb = 10 }
}

# Auto: a reserved size, placed wherever convenient.
resource "peernode_kubernetes_cluster" "api" {
  name = "api"
  mode = "auto"
  auto = { cpu = 2, memory_gb = 4, disk_gb = 20 }
}

# Node pools: count × (cpu, memory, disk). The first pool is the default.
resource "peernode_kubernetes_cluster" "batch" {
  name = "batch"
  mode = "pools"
  pools = [
    { name = "general", count = 2, cpu = 1, memory_gb = 2, disk_gb = 10 },
    { name = "big", count = 1, cpu = 4, memory_gb = 8 },
  ]
}
