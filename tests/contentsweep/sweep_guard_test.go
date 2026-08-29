package contentsweep

// sweep_guard_test.go — the guard on the guard.
//
// A sweep reports "clean" in exactly the same words whether it read the whole
// repository or read nothing at all. A wrong repository root, an empty file
// list or a regular expression that stopped matching would all show up as a
// green check. This repository has already shipped one gate that reported pass
// for weeks while rendering nothing, so the two tests here exist to make that
// specific failure impossible: one proves the sweep really is looking at this
// tree, the other proves its detectors really do fire.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimumTrackedFiles is a floor, not a count, so ordinary growth and ordinary
// deletion never touch it. The repository tracks a few thousand files; a run
// that finds a few hundred has found the wrong tree or a broken index, and
// calling that clean would be a lie.
const minimumTrackedFiles = 2000

func TestSweepReallyReadsTheTrackedTree(t *testing.T) {
	root := repoRoot(t)

	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the resolved repository root %s has no go.mod, so it is not the root of this module: %v", root, err)
	}

	files := trackedFiles(t, root)
	if len(files) < minimumTrackedFiles {
		t.Fatalf("git listed only %d tracked files under %s, which is far below the floor of %d; the sweep is reading the wrong tree or an incomplete index, and its result cannot be trusted",
			len(files), root, minimumTrackedFiles)
	}

	// A count alone could be satisfied by a tree of anything. These two paths
	// are part of the sweep's whole reason to exist — the Go sources and the
	// workflow that runs the check — so their absence means the wrong tree.
	wantPresent := []string{"go.mod", ".github/workflows/ci.yml"}
	present := make(map[string]bool, len(files))
	for _, rel := range files {
		present[rel] = true
	}
	for _, rel := range wantPresent {
		if !present[rel] {
			t.Errorf("the tracked file list does not contain %s, so this is not the repository the sweep is meant to read", rel)
		}
	}

	// The sweep would be pointless if it excluded the code. Prove at least one
	// ordinary .go file outside this package is in the set it will read.
	scannableGo := 0
	for _, rel := range files {
		if strings.HasSuffix(rel, ".go") && excludedReason(rel) == "" {
			scannableGo++
		}
	}
	if scannableGo == 0 {
		t.Error("no .go file at all is in the set the sweep reads; a committed credential would most likely be in one, so this would be a sweep with its main job removed")
	}
}

func TestSweepDetectorsActuallyFire(t *testing.T) {
	// An unremarkable path that is in no allowlist.
	const plainPath = "internal/example/plain.go"

	// Every shape the sweep claims to catch, given to it on a line, must come
	// back. A regular expression that quietly stopped matching would otherwise
	// leave the whole sweep green and empty.
	mustFind := []struct {
		what string
		line string
	}{
		{"AWS access key id", `key := "AKIAQWERTYUIOPASDFGH"`},
		{"temporary AWS access key id", `key := "ASIAQWERTYUIOPASDFGH"`},
		{"GitHub token", `token := "ghp_qwertyuiopasdfghjklz"`},
		{"Slack token", `token := "xoxb-9876543210-zyxwvutsrqpon"`},
		{"private key block", `pem := "-----BEGIN RSA PRIVATE KEY-----"`},
		{"twelve-digit AWS account id", `account := "489217530466"`},
		{"email address on a domain the project does not use", `owner = "someone@acmecorp-internal.com"`},
	}
	for _, tc := range mustFind {
		got := violationsInLine(plainPath, 7, tc.line)
		if len(got) == 0 {
			t.Errorf("the sweep reported nothing for a line carrying a %s (%q); that shape would be published unnoticed", tc.what, tc.line)
			continue
		}
		for _, finding := range got {
			if !strings.HasPrefix(finding, plainPath+":7: ") {
				t.Errorf("finding for a %s does not name the file and line it came from: %q", tc.what, finding)
			}
		}
	}

	// Ordinary content must not be reported, or the sweep is unusable and gets
	// switched off, which is the same as not having it.
	mustPass := []string{
		`contact := "maintainer@example.com"`,
		`account := "123456789012"`,
		`repo := "https://github.com/owner/repo"`,
		`version := "1234567890123"`,
	}
	for _, line := range mustPass {
		if got := violationsInLine(plainPath, 1, line); len(got) > 0 {
			t.Errorf("the sweep reported ordinary content as a violation: %q gave %v", line, got)
		}
	}

	// The exception is the pair, so prove both halves are load-bearing.
	const listedFile = "internal/orchestrator/ai_guard_test.go"
	const listedValue = "AKIAIOSFODNN7EXAMPLE"

	if got := violationsInLine(listedFile, 3, `key := "`+listedValue+`"`); len(got) != 0 {
		t.Errorf("a listed sentinel in the file it is listed for was reported anyway: %v", got)
	}
	if got := violationsInLine(plainPath, 3, `key := "`+listedValue+`"`); len(got) == 0 {
		t.Errorf("a listed sentinel copied into %s was NOT reported; the file half of the (path, text) pair is not being enforced", plainPath)
	}
	if got := violationsInLine(listedFile, 3, `token := "ghp_zzzzzzzzzzzzzzzzzzzz"`); len(got) == 0 {
		t.Errorf("a brand-new credential shape inside the already-listed file %s was NOT reported; being on the list has become a blanket pass for that file", listedFile)
	}
}
