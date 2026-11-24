// Command terraform-provider-peernode is the Terraform provider for PeerNode (https://peernode.cloud).
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/peernode-cloud/terraform-provider-peernode/internal/provider"
)

// version is set by goreleaser.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()
	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/peernode-cloud/peernode",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
