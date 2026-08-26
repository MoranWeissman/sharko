// catalog_credential_mutator_test.go — replaces catalog_credential_roundtrip_test.go
// at the layer that actually rewrites the operator's file.
//
// The mutators are what an "upgrade this addon" or "add this addon" request
// runs, and they rewrite the WHOLE catalog, not just the entry being changed.
// So every entry in the file is marshalled back out on every single write.
//
// The old test here required a stored password to survive that. It is gone for
// the reason set out in internal/config's replacement: a plaintext token in a
// file committed to Git cannot be made safe by handling it carefully, because
// Git keeps it in every clone, fork, CI cache and backup for good.
//
// What these tests require instead:
//
//   - ordinary catalog content still survives a whole-file rewrite untouched
//     (the data-loss worry the old test was built on is still real);
//   - a mutator asked to write an address carrying sign-in details writes
//     nothing at all;
//   - a mutator run over a catalog that ALREADY carries such an address
//     writes nothing at all either — Sharko does not quietly clean up an
//     operator's repository, and it does not quietly drop the entry. The
//     file stays exactly as it is until the operator migrates it.
package gitops

import (
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/models"
)

// mutatorSecret is the stand-in for an operator's stored password.
const mutatorSecret = "P4ss-w0rd-the-operator-actually-stored-7k4b"

// mutatorSweptSecret is the same text written independently, so the fixture
// and the sweep do not read one constant between them.
const mutatorSweptSecret = "P4ss-w0rd-the-operator-actually-stored-7k4b"

const mutatorBadRepoURL = "https://git-user:" + mutatorSecret + "@charts.example/org/charts"
const mutatorCleanRepoURL = "https://charts.example/org/charts"

func TestMutatorSentinelsAgree(t *testing.T) {
	if mutatorSecret != mutatorSweptSecret || mutatorSecret == "" {
		t.Fatalf("the planted secret and the swept secret disagree, or are empty:\n  planted %q\n  swept   %q", mutatorSecret, mutatorSweptSecret)
	}
}

// cleanCatalog builds a two-entry catalog with nothing objectionable in it.
func cleanCatalog(t *testing.T) []byte {
	t.Helper()
	data, err := config.MarshalAddonCatalog("addon-catalog", []models.AddonCatalogEntry{
		{Name: "keda", RepoURL: mutatorCleanRepoURL, Chart: "keda", Version: "2.13.0", Namespace: "keda"},
		{Name: "cert-manager", RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5", Namespace: "cert-manager"},
	})
	if err != nil {
		t.Fatalf("building the starting catalog: %v", err)
	}
	if !strings.Contains(string(data), mutatorCleanRepoURL) {
		t.Fatal("the starting catalog does not carry the untouched entry's address — this test would prove nothing")
	}
	return data
}

// catalogAlreadyCarryingACredential builds, by hand, the file an operator may
// already have in Git: one whose repoURL has sign-in details in it.
//
// It cannot be built with MarshalAddonCatalog — that is the point of this
// story, the writer refuses — so the YAML is assembled directly, the way the
// operator's editor would have.
func catalogAlreadyCarryingACredential(t *testing.T) []byte {
	t.Helper()
	body := config.AddonCatalogSchemaHeader + `
apiVersion: sharko.io/v1
kind: AddonCatalog
metadata:
    name: addon-catalog
spec:
    applicationsets:
        - name: keda
          repoURL: ` + mutatorBadRepoURL + `
          chart: keda
          version: 2.13.0
          namespace: keda
        - name: cert-manager
          repoURL: https://charts.jetstack.io
          chart: cert-manager
          version: 1.14.5
          namespace: cert-manager
`
	// Positive control, before anything is asserted about absence: the file
	// really does carry the secret, and the sweep really can see it.
	if !strings.Contains(body, mutatorSweptSecret) {
		t.Fatal("the hand-built catalog does not carry the planted secret — every check built on it would pass for the wrong reason")
	}
	return []byte(body)
}

func TestMutators_KeepAnUntouchedEntryByteIdentical(t *testing.T) {
	start := cleanCatalog(t)

	t.Run("UpdateCatalogVersion on another addon", func(t *testing.T) {
		out, err := UpdateCatalogVersion(start, "cert-manager", "1.15.0")
		if err != nil {
			t.Fatalf("UpdateCatalogVersion: %v", err)
		}
		if !strings.Contains(string(out), "1.15.0") {
			t.Fatal("the version was not changed — the mutator never ran, so the check below proves nothing")
		}
		if !strings.Contains(string(out), mutatorCleanRepoURL) {
			t.Errorf("the untouched entry's address is gone after a version bump. Sharko rewrites the whole file and has no second copy.\n\nthe file was:\n%s", out)
		}
	})

	t.Run("AddCatalogEntry", func(t *testing.T) {
		out, err := AddCatalogEntry(start, CatalogEntryInput{
			Name: "external-dns", RepoURL: "https://kubernetes-sigs.github.io/external-dns/",
			Chart: "external-dns", Version: "1.14.0",
		})
		if err != nil {
			t.Fatalf("AddCatalogEntry: %v", err)
		}
		if !strings.Contains(string(out), "external-dns") {
			t.Fatal("the addon was not added — the mutator never ran")
		}
		if !strings.Contains(string(out), mutatorCleanRepoURL) {
			t.Errorf("the untouched entry's address is gone after an add:\n%s", out)
		}
	})

	t.Run("RemoveCatalogEntry", func(t *testing.T) {
		out, err := RemoveCatalogEntry(start, "cert-manager")
		if err != nil {
			t.Fatalf("RemoveCatalogEntry: %v", err)
		}
		if strings.Contains(string(out), "cert-manager") {
			t.Fatal("the addon was not removed — the mutator never ran")
		}
		if !strings.Contains(string(out), mutatorCleanRepoURL) {
			t.Errorf("the untouched entry's address is gone after a removal:\n%s", out)
		}
	})

	t.Run("a no-op round trip is byte-identical", func(t *testing.T) {
		entries, err := catalogParser.ParseAddonsCatalog(start)
		if err != nil {
			t.Fatalf("parsing: %v", err)
		}
		out, err := config.MarshalAddonCatalog("addon-catalog", entries)
		if err != nil {
			t.Fatalf("marshalling: %v", err)
		}
		if string(out) != string(start) {
			t.Errorf("the catalog changed on a no-op round trip.\n\nbefore:\n%s\nafter:\n%s", start, out)
		}
	})
}

// TestMutators_RefuseToWriteACarrier covers a request that tries to PUT such
// an address into the file.
func TestMutators_RefuseToWriteACarrier(t *testing.T) {
	start := cleanCatalog(t)

	cases := map[string]func() ([]byte, error){
		"AddCatalogEntry": func() ([]byte, error) {
			return AddCatalogEntry(start, CatalogEntryInput{
				Name: "external-dns", RepoURL: mutatorBadRepoURL, Chart: "external-dns", Version: "1.14.0",
			})
		},
		"UpdateCatalogEntry": func() ([]byte, error) {
			return UpdateCatalogEntry(start, "keda", map[string]string{"repoURL": mutatorBadRepoURL})
		},
	}
	if len(cases) == 0 {
		t.Fatal("no mutators under test")
	}

	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := run()
			assertMutatorRefused(t, out, err)
		})
	}
}

// TestMutators_RefuseToRewriteAFileThatAlreadyCarriesOne is the point-7 proof
// at the write layer: an ordinary, unrelated change to a catalog that already
// carries such an address is refused, so the operator's file is never rewritten
// without it and the entry is never quietly dropped.
func TestMutators_RefuseToRewriteAFileThatAlreadyCarriesOne(t *testing.T) {
	start := catalogAlreadyCarryingACredential(t)

	cases := map[string]func() ([]byte, error){
		"a version bump on a completely different addon": func() ([]byte, error) {
			return UpdateCatalogVersion(start, "cert-manager", "1.15.0")
		},
		"adding an unrelated addon": func() ([]byte, error) {
			return AddCatalogEntry(start, CatalogEntryInput{
				Name: "external-dns", RepoURL: "https://kubernetes-sigs.github.io/external-dns/",
				Chart: "external-dns", Version: "1.14.0",
			})
		},
		"removing an unrelated addon": func() ([]byte, error) {
			return RemoveCatalogEntry(start, "cert-manager")
		},
	}
	if len(cases) != 3 {
		t.Fatalf("expected exactly 3 ordinary changes under test, have %d", len(cases))
	}

	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := run()
			assertMutatorRefused(t, out, err)
		})
	}
}

func assertMutatorRefused(t *testing.T, out []byte, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("the rewrite was allowed. These bytes would be committed to the operator's repository:\n%s", out)
	}
	if len(out) != 0 {
		t.Errorf("the rewrite was refused but still handed back %d bytes", len(out))
	}

	var typed *credsafe.UnsupportedRepoURLError
	if !errors.As(err, &typed) {
		t.Fatalf("the refusal is not a *credsafe.UnsupportedRepoURLError — a caller would have to read the English: %v", err)
	}
	if typed.File == "" || typed.Field == "" {
		t.Errorf("the refusal does not say where the problem is: file %q field %q", typed.File, typed.Field)
	}
	msg := err.Error()
	if strings.Contains(msg, mutatorSweptSecret) {
		t.Error("the refusal repeats the value it refused")
	}
	if strings.Contains(msg, mutatorSweptSecret[:8]) {
		t.Error("the refusal repeats the first eight characters of the value it refused")
	}
	if strings.Contains(msg, "git-user") {
		t.Error("the refusal repeats the user half of the address")
	}
}
