# The current way to declare a module.
import "module" "tfplan-functions" {
  source = "./modules/tfplan-functions.sentinel"
}

# The older bare block, still found in existing policy sets.
module "legacy-helpers" {
  source = "./modules/legacy-helpers.sentinel"
}

import "plugin" "time" {
  config = {
    timezone = "Europe/Paris"
  }
}

import "plugin" "flipper" {
  source = "./plugins/flipper"
  args   = ["-verbose"]

  env = {
    FLIPPER_LOG = "debug"
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
    label   = "production"
  }
}

# No enforcement_level: Sentinel falls back to advisory.
policy "require-tags" {
  source = "./require-tags.sentinel"
}

policy "remote-policy" {
  source            = "git::https://github.com/hashicorp/sentinel-example.git//main.sentinel"
  enforcement_level = "soft-mandatory"
}

# A block this provider does not model. Parsing must ignore it rather than fail.
mock "tfplan/v2" {
  module {
    source = "./mocks/mock-tfplan-v2.sentinel"
  }
}

global "environment" {
  value = "production"
}
