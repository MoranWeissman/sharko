//go:build e2e

// Package harness provides kind-cluster lifecycle primitives for the
// Sharko Go end-to-end test suite (V2 Epic 7-1).
//
// # Topology model
//
// Every harness-provisioned topology consists of one "mgmt" cluster (where
// sharko + ArgoCD will be installed in story 7-1.2) plus N "target" clusters
// (the clusters Sharko manages). All clusters in a single ProvisionTopology
// call share a generated RunID so they can be correlated and torn down as
// a unit, even from a separate process.
//
// Cluster names follow the pattern:
//
//	sharko-e2e-{role}-{runID}
//
// where role is "mgmt" or "target-N" (1-indexed) and runID is a short
// timestamp+random tag (~10 chars). Kubectl contexts are the kind default
// of "kind-<cluster-name>". Per-cluster kubeconfig files are written under
// t.TempDir() so they vanish on test cleanup.
//
// # Sentinel-label safety contract
//
// EVERY node in EVERY harness-provisioned cluster carries three labels:
//
//   - e2e.sharko.io/test=true        (the safe-to-destroy sentinel)
//   - e2e.sharko.io/run-id=<RunID>   (groups one ProvisionTopology call)
//   - e2e.sharko.io/role=<role>      (mgmt | target-1 | target-2 | ...)
//
// DestroyAllStaleE2EClusters relies on the sentinel: it enumerates every
// kind cluster on the host, probes each one's nodes for the sentinel, and
// destroys ONLY clusters that carry it. Clusters without the sentinel
// (the maintainer's dev clusters, sharko-target-N from sharko-dev.sh,
// etc.) are never touched. This is a hard invariant — never broaden the
// destroy criteria beyond the sentinel.
//
// # Environment variables (all optional)
//
//   - E2E_KIND_IMAGE     kindest/node image to provision (default: kindest/node:v1.31.0)
//   - E2E_KIND_BIN       path to the kind binary (default: "kind" on PATH)
//   - E2E_KUBECTL_BIN    path to the kubectl binary (default: "kubectl" on PATH)
//
// Tests skip themselves when kind / kubectl / docker are not present on PATH
// rather than failing — this keeps the suite friendly to environments where
// only a subset of tooling is installed (e.g. a CI matrix entry that only
// runs unit tests).
//
// # Build tag
//
// All files in this package carry the //go:build e2e build tag. Default
// `go test ./...` excludes them; the suite is opted in via:
//
//	go test -tags=e2e ./tests/e2e/...
//
// # Downstream stories
//
// Stories 7-1.2 (sharko boot + git fixture), 7-1.3 (API client + auth),
// and 7-1.4..7-1.13 (per-domain lifecycle tests) all import this package.
// The function signatures here are the stable contract — change them with
// care.
package harness
