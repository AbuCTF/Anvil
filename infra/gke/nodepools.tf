# platform pool: small, on-demand, always-on. hosts system pods + Anvil.
resource "google_container_node_pool" "platform" {
  name       = "platform"
  cluster    = google_container_cluster.anvil.id
  location   = var.zone
  node_count = 1

  node_config {
    machine_type = "e2-standard-4" # 4 vCPU / 16 GB
    disk_size_gb = 40
    disk_type    = "pd-balanced"
    oauth_scopes = ["https://www.googleapis.com/auth/cloud-platform"]

    workload_metadata_config {
      mode = "GKE_METADATA" # metadata server locked via Workload Identity
    }
    shielded_instance_config {
      enable_secure_boot          = true
      enable_integrity_monitoring = true
    }
    labels = { pool = "platform" }
  }

  management {
    auto_repair  = true
    auto_upgrade = true
  }
}

# challenge pool: Spot + high-memory (challenges are RAM-bound, CPU-overcommitted),
# scale-to-zero when idle so it costs nothing between test runs.
resource "google_container_node_pool" "challenges" {
  provider = google-beta # sandbox_config (GKE Sandbox / gVisor) is beta-gated
  name     = "challenges"
  cluster  = google_container_cluster.anvil.id
  location = var.zone

  autoscaling {
    min_node_count = 0 # zero when idle => free
    max_node_count = 2 # 2 x e2-highmem-8 = 16 vCPU / 128 GB
  }

  node_config {
    machine_type = "e2-highmem-8" # 8 vCPU / 64 GB — RAM for the idle-instance fleet
    spot         = true           # up to 91% off
    disk_size_gb = 50
    disk_type    = "pd-balanced"
    oauth_scopes = ["https://www.googleapis.com/auth/cloud-platform"]

    workload_metadata_config {
      mode = "GKE_METADATA"
    }
    shielded_instance_config {
      enable_secure_boot          = true
      enable_integrity_monitoring = true
    }
    labels = { pool = "challenges" }

    # gVisor sandbox: untrusted challenge code runs in a userspace kernel, never
    # the host kernel. GKE auto-labels these nodes sandbox.gke.io/runtime=gvisor
    # and taints them sandbox.gke.io/runtime=gvisor:NoSchedule — that taint is the
    # isolation boundary (only runtimeClassName=gvisor pods tolerate it), so no
    # separate custom taint is needed. The RuntimeClass injects the matching
    # nodeSelector + toleration, and the autoscaler grows this pool from zero.
    sandbox_config {
      sandbox_type = "gvisor"
    }
  }

  management {
    auto_repair  = true
    auto_upgrade = true
  }
}
