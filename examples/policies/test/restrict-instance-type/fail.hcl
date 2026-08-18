mock "tfplan/v2" {
  module {
    source = "./mock-tfplan-v2-fail.sentinel"
  }
}

param "allowed_types" {
  value = ["t3.micro", "t3.small"]
}

test {
  rules = {
    main = false
  }
}
