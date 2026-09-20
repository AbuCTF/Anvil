output "cluster_name" {
  value = google_container_cluster.anvil.name
}

output "cluster_endpoint" {
  value     = google_container_cluster.anvil.endpoint
  sensitive = true
}

# run this to point kubectl at the cluster.
output "get_credentials" {
  value = "gcloud container clusters get-credentials ${google_container_cluster.anvil.name} --zone ${var.zone} --project ${var.project_id}"
}
