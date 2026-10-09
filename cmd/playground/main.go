// Command playground orchestrates a local kind-based playground for
// proving the full Sharko GitOps loop (hub + spoke clusters, ArgoCD +
// Sharko + Gitea) without needing EKS or any cloud secret backend.
//
// This command handles ONLY the `up` subcommand — spinning up the full
// topology (hub + N spoke clusters, ArgoCD + Sharko + GitFake, and
// registering the spokes as Sharko-managed clusters).
//
// Other operations (status, tunnels, down) are implemented as Makefile
// targets that call helm/kubectl/bash directly:
//   - make playground-status
//   - make playground-tunnels
//   - make playground-down
//
// This is a dev-tooling command that shells out to kind/kubectl/helm/docker
// and reuses the untagged GitFake core. It is NOT behind the e2e build tag
// so `go build ./...` always compiles it.
package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s up\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nOther operations:\n")
		fmt.Fprintf(os.Stderr, "  make playground-status      # Show current state\n")
		fmt.Fprintf(os.Stderr, "  make playground-tunnels     # Open browser tunnels\n")
		fmt.Fprintf(os.Stderr, "  make playground-down        # Tear down clusters\n")
		os.Exit(1)
	}

	ctx := context.Background()
	subcommand := os.Args[1]

	var err error
	switch subcommand {
	case "up":
		err = cmdUp(ctx)
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n", subcommand)
		fmt.Fprintf(os.Stderr, "This command only handles 'up'. For other operations, use the Makefile targets:\n")
		fmt.Fprintf(os.Stderr, "  make playground-status\n")
		fmt.Fprintf(os.Stderr, "  make playground-tunnels\n")
		fmt.Fprintf(os.Stderr, "  make playground-down\n")
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
