# enable every API the platform needs. idempotent — already-enabled stays enabled.
# disable_on_destroy=false so a `terraform destroy` of the cluster doesn't rip
# APIs out from under anything else in the project.
locals {
  services = [
    "compute.googleapis.com",            # VMs / networking / GKE nodes
    "container.googleapis.com",          # GKE
    "artifactregistry.googleapis.com",   # challenge + platform images
    "servicenetworking.googleapis.com",  # private services access (Cloud SQL/Redis private IP)
    "sqladmin.googleapis.com",           # Cloud SQL (Postgres)
    "redis.googleapis.com",              # Memorystore (Redis)
    "certificatemanager.googleapis.com", # managed certs (option alongside cert-manager)
    "monitoring.googleapis.com",
    "logging.googleapis.com",
    "cloudquotas.googleapis.com", # quota increase requests
    "cloudbilling.googleapis.com",
  ]
}

resource "google_project_service" "enabled" {
  for_each = toset(local.services)

  service            = each.value
  disable_on_destroy = false
}
