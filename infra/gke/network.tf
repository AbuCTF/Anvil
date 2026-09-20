# custom VPC — one subnet in the region, VPC-native (pods/services as secondary
# ranges), private nodes with egress via Cloud NAT on a single IP.

resource "google_compute_network" "vpc" {
  name                    = "anvil-vpc"
  auto_create_subnetworks = false
  depends_on              = [google_project_service.enabled]
}

resource "google_compute_subnetwork" "subnet" {
  name          = "anvil-${var.region}"
  network       = google_compute_network.vpc.id
  region        = var.region
  ip_cidr_range = var.nodes_cidr

  # nodes have no external IPs; this lets them reach Google APIs privately.
  private_ip_google_access = true

  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = var.pods_cidr
  }
  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = var.services_cidr
  }
}

# --- egress for private nodes: one Cloud NAT IP for the whole cluster ------
# one NAT IP provides ~64k ports => ~1000 VMs of egress; auto-only scales IPs
# only if we ever exceed that (we won't at this scale), so it stays at 1 IP.
resource "google_compute_router" "router" {
  name    = "anvil-router"
  region  = var.region
  network = google_compute_network.vpc.id
}

resource "google_compute_router_nat" "nat" {
  name                               = "anvil-nat"
  router                             = google_compute_router.router.name
  region                             = var.region
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"

  log_config {
    enable = true
    filter = "ERRORS_ONLY"
  }
}

# --- firewall -------------------------------------------------------------
# GKE manages most node rules itself; we add the essentials.

# allow traffic within the VPC (nodes <-> pods <-> services).
resource "google_compute_firewall" "allow_internal" {
  name    = "anvil-allow-internal"
  network = google_compute_network.vpc.name

  allow {
    protocol = "tcp"
  }
  allow {
    protocol = "udp"
  }
  allow {
    protocol = "icmp"
  }
  source_ranges = [var.nodes_cidr, var.pods_cidr, var.services_cidr]
}

# allow GCP load-balancer health checks to reach node ports.
resource "google_compute_firewall" "allow_health_checks" {
  name    = "anvil-allow-health-checks"
  network = google_compute_network.vpc.name

  allow {
    protocol = "tcp"
  }
  # GCP health-check + LB source ranges.
  source_ranges = ["35.191.0.0/16", "130.211.0.0/22"]
}

# allow SSH to nodes only via IAP (no public SSH surface).
resource "google_compute_firewall" "allow_iap_ssh" {
  name    = "anvil-allow-iap-ssh"
  network = google_compute_network.vpc.name

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }
  source_ranges = ["35.235.240.0/20"] # Google IAP range
}
