terraform {
  required_version = ">= 1.8.0"

  required_providers {
    sentinel = {
      source = "kevineor/sentinel"
    }
  }
}

# The provider takes no configuration. The Sentinel CLI is looked up in PATH as
# `sentinel`, unless the SENTINEL_BINARY environment variable points elsewhere.
provider "sentinel" {}
