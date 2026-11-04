package main

import (
	"context"
	"flag"
	"log"

	"github.com/Lenstra/terraform-provider-sentinel/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

// version is set by the release build.
var version = "dev"

func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/Lenstra/sentinel",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err.Error())
	}
}
