# The current way to declare a module.
import "module" "helpers" {
  source = "./modules/helpers.sentinel"
}

# The older bare block, still found in existing policy sets.
module "legacy" {
  source = "./modules/legacy.sentinel"
}

import "plugin" "time" {
  config = {
    timezone = "Europe/Paris"
  }
}

import "static" "people" {
  source = "./data/people.json"
  format = "json"
}

policy "restrict-instance-type" {
  source            = "./restrict-instance-type.sentinel"
  enforcement_level = "hard-mandatory"

  params = {
    allowed = ["t3.micro", "t3.small"]
    max     = 2
  }
}

# No enforcement_level: Sentinel falls back to advisory.
policy "require-tags" {
  source = "./require-tags.sentinel"
}

# Not a local path, so there is nothing on disk to read.
policy "shared-policy" {
  source            = "git::https://github.com/hashicorp/sentinel-example.git//main.sentinel"
  enforcement_level = "soft-mandatory"
}
