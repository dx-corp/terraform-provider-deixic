package main

import (
	"context"
	"flag"
	"log"

	"github.com/dx-corp/terraform-provider-deixic/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with debugger support")
	flag.Parse()

	err := providerserver.Serve(
		context.Background(),
		provider.New(version),
		providerserver.ServeOpts{
			Address: "registry.terraform.io/dx-corp/deixic",
			Debug:   debug,
		},
	)
	if err != nil {
		log.Fatal(err)
	}
}
