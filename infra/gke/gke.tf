# the cluster. zonal => qualifies for the one free cluster (no $0.10/hr fee).
# VPC-native, Dataplane V2 (Cilium, NetworkPolicy always-on), private nodes.

resource "google_container_cluster" "anvil" {
  name     = "anvil"
  location = var.zone

  network    = google_compute_network.vpc.id
  subnetwork = google_compute_subnetwork.subnet.id

  # we run our own node pools; drop the default.
  remove_default_node_pool = true
  initial_node_count       = 1

  networking_mode = "VPC_NATIVE"
  ip_allocation_policy {
    cluster_secondary_range_name  = "pods"
    services_secondary_range_name = "services"
  }

  # Cilium/eBPF datapath -> NetworkPolicy is always enforced (default-deny egress later).
  datapath_provider = "ADVANCED_DATAPATH"

  # nodes have no public IPs; egress goes through Cloud NAT.
  private_cluster_config {
    enable_private_nodes    = true
    enable_private_endpoint = false
    master_ipv4_cidr_block  = "172.16.0.0/28"
  }

  # pods assume GCP identity without node keys (locks the metadata server).
  workload_identity_config {
    workload_pool = "${var.project_id}.svc.id.goog"
  }

  release_channel {
    channel = "REGULAR"
  }

  # we admin the API from atom; open for now, tighten to atom's IP later.
  master_authorized_networks_config {
    cidr_blocks {
      cidr_block   = "0.0.0.0/0"
      display_name = "admin-open-tighten-later"
    }
  }

  deletion_protection = false

  depends_on = [google_compute_router_nat.nat]
}
