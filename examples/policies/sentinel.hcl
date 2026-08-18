policy "restrict-instance-type" {
  source            = "./restrict-instance-type.sentinel"
  enforcement_level = "hard-mandatory"

  params = {
    allowed_types = ["t3.micro", "t3.small"]
  }
}

policy "require-tags" {
  source            = "./require-tags.sentinel"
  enforcement_level = "soft-mandatory"
}
