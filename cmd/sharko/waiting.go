package main

import "fmt"

// A command that opens a pull request which still waits for a merge has not
// finished the job. These helpers keep the words for that in one place, so
// remove-cluster, unadopt-cluster and adopt all say the same thing.

// prWaiting is true when a pull request was opened and is not merged yet.
func prWaiting(prURL string, merged bool) bool {
	return prURL != "" && !merged
}

// waitingForMergeLine is the one line that says a pull request waits.
func waitingForMergeLine(prURL string) string {
	return fmt.Sprintf("PR opened, waiting for merge: %s", prURL)
}

// waitingProgressWord ends a "Doing the thing... " progress line when the
// pull request waits. It is not "done": nothing has changed yet.
const waitingProgressWord = "waiting for merge"
