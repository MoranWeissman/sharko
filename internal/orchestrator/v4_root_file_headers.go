// Package orchestrator — plain-English comment headers for the v4 root
// files Sharko generates (v4 naming polish, item 3).
//
// The maintainer who reads these files day to day is not a native English
// speaker, so every header is a handful of short, plain sentences at the
// very top explaining what the file is for — no jargon, nothing you need
// Sharko's docs to decode.
//
// Every header rides CREATION only: it is written the first time Sharko
// creates a file that did not exist before. None of the mutators that touch
// these files afterwards (AddClusterEntry, SetClusterAddonsAddon, ...)
// preserve arbitrary comments across an ordinary edit — they round-trip
// through a typed Go struct, not a surgical text splice — so a header added
// here is a one-time welcome note for whoever opens the file first, not a
// promise that survives every future edit. catalog.yaml is the one
// exception: its writer (catalog_yaml_edit.go) DOES edit surgically, so a
// header written there the day the file is created keeps riding along on
// every ordinary addition afterwards, untouched, exactly like every other
// hand-written comment in that file.
package orchestrator

import "fmt"

// managedClustersFileHeader is the header written the first time Sharko
// creates managed-clusters.yaml (a brand-new repo's first cluster
// registration or takeover).
const managedClustersFileHeader = "" +
	"# The clusters Sharko manages.\n" +
	"# Registering or removing a cluster through Sharko connects it to ArgoCD, or disconnects it.\n" +
	"# Day-to-day addon changes never touch this file — see " + V4ClustersDir + "/<cluster>.yaml for that.\n"

// catalogYAMLHeader is the header written the first time Sharko creates
// catalog.yaml. Because catalog.yaml is edited surgically (splice, not a
// full remarshal — see catalog_yaml_edit.go), this header keeps riding
// along on every ordinary catalog change after it is written once.
const catalogYAMLHeader = "" +
	"# The addons your org has approved.\n" +
	"# Only an addon listed here can be turned on for a cluster.\n" +
	"# Adding an addon here happens through a pull request somebody reviews.\n"

// sharkoEngineYAMLHeader is the header written into the engine pin the day
// the v4 bootstrap seed is committed — sharko-engine.yaml is created exactly
// once per repo.
const sharkoEngineYAMLHeader = "" +
	"# The one machinery file: the ArgoCD Application that runs Sharko's engine chart, at a pinned version.\n" +
	"# Sharko opens a pull request to bump the pinned version — nothing else in this file changes on its own.\n"

// clusterAddonsFileHeader is the header written the first time Sharko
// creates a cluster's cluster-addons/<name>.yaml assignment file. Named to
// the cluster so the note is about the exact file it sits in.
func clusterAddonsFileHeader(clusterName string) string {
	return fmt.Sprintf(""+
		"# Which approved addons run on %s.\n"+
		"# Turning an addon on or off for this cluster is done by editing this file.\n",
		clusterName)
}
