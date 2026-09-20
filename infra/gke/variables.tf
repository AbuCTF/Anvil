variable "project_id" {
  type        = string
  description = "GCP project hosting the CTF."
}

variable "region" {
  type    = string
  default = "asia-south1" # Mumbai — closest to most players; Cloudflare fronts the rest.
}

variable "zone" {
  type    = string
  default = "asia-south1-a" # zonal cluster => the one free cluster (no $0.10/hr mgmt fee).
}

# --- IP plan (see infra design doc) ---------------------------------------
# GKE pods/services live in 100.64.0.0/10 so they never collide with the
# 10.x ranges the WireGuard VPN (10.64/12) and per-team VM subnets (10.80/12)
# use on the atom side.

variable "nodes_cidr" {
  type    = string
  default = "10.128.0.0/20" # node primary range (does not overlap VPN/VM 10.64-10.95)
}

variable "pods_cidr" {
  type    = string
  default = "100.64.0.0/14" # secondary range for pods (~260k IPs)
}

variable "services_cidr" {
  type    = string
  default = "100.68.0.0/20" # secondary range for services (~4k IPs)
}
