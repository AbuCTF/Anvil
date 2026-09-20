# managed data services — Postgres (Cloud SQL) + Redis (Memorystore).
# both bill against their OWN quotas, not the 24-vCPU Compute quota, so the whole
# cluster stays free for pods. reached over private IP via VPC peering (PSA).

# --- private services access: peer the VPC with Google's managed-services net --
resource "google_compute_global_address" "psa" {
  name          = "anvil-psa-range"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 20
  network       = google_compute_network.vpc.id
}

resource "google_service_networking_connection" "psa" {
  network                 = google_compute_network.vpc.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.psa.name]
  depends_on              = [google_project_service.enabled]
}

# --- Postgres (Cloud SQL) --------------------------------------------------
resource "random_password" "db" {
  length  = 32
  special = false
}

resource "google_sql_database_instance" "anvil" {
  name             = "anvil-pg"
  database_version = "POSTGRES_16"
  region           = var.region

  depends_on = [google_service_networking_connection.psa]

  settings {
    edition           = "ENTERPRISE" # standard edition; ENTERPRISE_PLUS forces perf-optimized tiers
    tier              = "db-custom-1-3840" # 1 vCPU / 3.75 GB — small, stoppable when idle
    availability_type = "ZONAL"
    disk_size         = 20
    disk_autoresize   = true

    ip_configuration {
      ipv4_enabled    = false # private IP only
      private_network = google_compute_network.vpc.id
    }

    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
    }
  }

  deletion_protection = false
}

resource "google_sql_database" "anvil" {
  name     = "anvil"
  instance = google_sql_database_instance.anvil.name
}

resource "google_sql_user" "anvil" {
  name     = "anvil"
  instance = google_sql_database_instance.anvil.name
  password = random_password.db.result
}

# --- Redis (Memorystore Basic, 1 GB) --------------------------------------
resource "google_redis_instance" "anvil" {
  name               = "anvil-redis"
  tier               = "BASIC"
  memory_size_gb     = 1
  region             = var.region
  authorized_network = google_compute_network.vpc.id
  redis_version      = "REDIS_7_2"
  depends_on         = [google_project_service.enabled]
}

# --- outputs for wiring the app secret (consumed via `terraform output -raw`) --
output "db_host" { value = google_sql_database_instance.anvil.private_ip_address }
output "db_password" {
  value     = random_password.db.result
  sensitive = true
}
output "redis_host" { value = google_redis_instance.anvil.host }
