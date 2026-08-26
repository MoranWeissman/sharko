package argocd

// unreachable_leak_test.go — BF8. Proof that the ArgoCD server address, and
// anything an operator wrote into it, does not come back out of a failed call.
//
// The sweep, the sentinel and the four carrier shapes are shared with
// write_refused_leak_test.go (BF6): same package, same finder, same positive
// controls. A second copy of a leak sweep is a second thing to get wrong.

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// closedLoopbackPort opens a listener on the loopback interface, notes the
// port it was given, and closes it. Dialling that port fails immediately and
// reaches nothing outside this machine.
func closedLoopbackPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not open a loopback listener: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("could not close the loopback listener: %v", err)
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("could not read the port back from %q: %v", addr, err)
	}
	return port
}

// unreachableBaseURLs are the four carrier shapes written into a BASE ADDRESS
// rather than a repository address: this is the value an operator types into
// the ArgoCD server field, and all four are things they can type.
func unreachableBaseURLs(port string) []struct{ name, url string } {
	host := "127.0.0.1:" + port
	return []struct{ name, url string }{
		{"password slot", "https://x-access-token:" + writeLeakSentinel + "@" + host},
		{"username slot", "https://" + writeLeakSentinel + "@" + host},
		{"query parameter", "https://" + host + "?access_token=" + writeLeakSentinel},
		{"fragment", "https://" + host + "#" + writeLeakSentinel},
	}
}

// TestUnreachablePositiveControl_ABareClientStillQuotesTheAddress is the
// control, and nothing below may be believed before it passes.
//
// Every assertion in this file is an ABSENCE, and an absence is what a test
// dialling a port that quietly succeeds also reports. So the same four dials
// are made with a bare http.Client first. Three of the four must come back
// carrying the sentinel — proving the leak is still there for Sharko to stop.
//
// The password slot is asserted the other way round, honestly: net/http
// rewrites a password to "***" before it builds the error, so the standard
// library cleans that one shape and Sharko is not what makes it safe. Forcing
// that case to "leak" would be inventing a result. The three shapes net/http
// does nothing about are exactly why this fix had to exist.
func TestUnreachablePositiveControl_ABareClientStillQuotesTheAddress(t *testing.T) {
	port := closedLoopbackPort(t)
	for _, carrier := range unreachableBaseURLs(port) {
		t.Run(carrier.name, func(t *testing.T) {
			_, err := (&http.Client{}).Get(carrier.url + "/api/v1/clusters")
			if err == nil {
				t.Fatal("the dial to a closed loopback port succeeded — this file can prove nothing")
			}
			carriesIt := strings.Contains(err.Error(), writeLeakSentinel)
			if carrier.name == "password slot" {
				if carriesIt {
					t.Errorf("net/http stopped masking the password slot: %v", err)
				}
				return
			}
			if !carriesIt {
				t.Fatalf("a bare http.Client no longer quotes the %s, so the rest of this file sweeps for something that was never in play.\n\nthe error was:\n%v", carrier.name, err)
			}
		})
	}
}

// TestUnreachable_NoVerbLeaksTheAddressItDialled is the assertion BF8 exists
// for: all four HTTP verbs, all four carriers, both err.Error() and the %v
// formatting a boundary might reach for instead.
func TestUnreachable_NoVerbLeaksTheAddressItDialled(t *testing.T) {
	port := closedLoopbackPort(t)

	verbs := []struct {
		name string
		call func(c *Client) error
	}{
		// doGet
		{"GET", func(c *Client) error { _, err := c.ListClusters(context.Background()); return err }},
		// doPost
		{"POST", func(c *Client) error { return c.SyncApplication(context.Background(), "keda-prod-eu") }},
		// doPut
		{"PUT", func(c *Client) error {
			return c.UpdateClusterLabels(context.Background(), "https://k8s.example", map[string]string{"a": "b"})
		}},
		// doDelete
		{"DELETE", func(c *Client) error { return c.TerminateOperation(context.Background(), "keda-prod-eu") }},
	}

	for _, carrier := range unreachableBaseURLs(port) {
		for _, verb := range verbs {
			t.Run(carrier.name+"/"+verb.name, func(t *testing.T) {
				err := verb.call(NewClient(carrier.url, "a-token", false))
				if err == nil {
					t.Fatal("the call to a closed loopback port succeeded — the transport-failure branch never ran")
				}
				assertNoWriteLeak(t, verb.name+" err.Error() on an unreachable ArgoCD", err.Error(), carrier.url)
				assertNoWriteLeak(t, verb.name+" %v of the error on an unreachable ArgoCD", fmt.Sprintf("%v", err), carrier.url)
				// And %+v, which is what a caller reaching for "more detail"
				// types next.
				assertNoWriteLeak(t, verb.name+" %+v of the error on an unreachable ArgoCD", fmt.Sprintf("%+v", err), carrier.url)
			})
		}
	}
}

// TestUnreachable_KeepsWhatCallersBranchOn covers the three things the old
// wrapped chain gave callers, each of which is decided by a type or a sentinel
// rather than by reading words.
func TestUnreachable_KeepsWhatCallersBranchOn(t *testing.T) {
	port := closedLoopbackPort(t)
	base := "https://127.0.0.1:" + port

	t.Run("a cancelled call is still a cancelled call", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := NewClient(base, "tok", false).ListClusters(ctx)
		if err == nil {
			t.Fatal("a cancelled call succeeded")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("internal/audit can no longer see that this was a cancel: %v", err)
		}
	})

	t.Run("the error is a net.Error so audit can still classify it", func(t *testing.T) {
		err := NewClient(base, "tok", false).SyncApplication(context.Background(), "keda")
		if err == nil {
			t.Fatal("the call to a closed loopback port succeeded")
		}
		var netErr net.Error
		if !errors.As(err, &netErr) {
			t.Error("the error is not a net.Error any more — internal/audit's transport branch stops telling a timeout apart from an unreachable host")
		}
	})

	t.Run("the endpoint is still named, so triage did not get worse", func(t *testing.T) {
		err := NewClient(base, "tok", false).SyncApplication(context.Background(), "keda-prod-eu")
		if err == nil {
			t.Fatal("the call to a closed loopback port succeeded")
		}
		if !strings.Contains(err.Error(), "/api/v1/applications/keda-prod-eu/sync") {
			t.Errorf("the error no longer says which call failed: %q", err.Error())
		}
	})
}

// TestSafeReadFailure_SaysTheRightThingWithoutTheAddress covers the boundary
// helper directly.
func TestSafeReadFailure_SaysTheRightThingWithoutTheAddress(t *testing.T) {
	port := closedLoopbackPort(t)

	for _, carrier := range unreachableBaseURLs(port) {
		t.Run(carrier.name, func(t *testing.T) {
			_, err := NewClient(carrier.url, "tok", false).ListClusters(context.Background())
			if err == nil {
				t.Fatal("the call to a closed loopback port succeeded")
			}
			sentence := SafeReadFailure(err)
			if !strings.Contains(sentence, "Sharko could not get an answer from ArgoCD") {
				t.Errorf("an unreachable ArgoCD did not produce the unreachable sentence: %q", sentence)
			}
			// It must not borrow the WRITE sentence, which claims Sharko does
			// not know whether anything was applied. A read applies nothing.
			if strings.Contains(sentence, "whether anything was applied") {
				t.Errorf("a failed READ told the operator something might have been applied: %q", sentence)
			}
			assertNoWriteLeak(t, "SafeReadFailure on an unreachable ArgoCD", sentence, carrier.url)
		})
	}
}

// --- the guard --------------------------------------------------------------

// wantRoundTripSites is THE LIST: every place in this package that makes an
// HTTP round trip, and every place that reads the base address. Each is one
// file plus one enclosing function.
//
// It is a list and not a count on purpose. A count agrees with a site that
// moved; the list disagrees twice — one entry stale, one site unlisted — and
// says which.
var wantRoundTripSites = []roundTripSite{
	{file: "client.go", fn: "doGet"},
	{file: "client_write.go", fn: "doDelete"},
	{file: "client_write.go", fn: "doPost"},
	{file: "client_write.go", fn: "doPut"},
}

type roundTripSite struct{ file, fn string }

func (s roundTripSite) String() string { return s.file + ":" + s.fn }

// TestEveryRoundTripGoesThroughUnreachableCallError walks this package's
// shipping source and requires that every function making an HTTP round trip
// is on the list above AND routes its failure through unreachableCallError.
//
// Direction check: this test gets ANGRIER as the bug reappears. A new verb
// added without the helper is an unlisted site (failure) and a function
// missing the helper call (failure). It cannot get quieter.
func TestEveryRoundTripGoesThroughUnreachableCallError(t *testing.T) {
	files := shippingGoFiles(t, ".")
	if len(files) == 0 {
		t.Fatal("the walk read no Go files at all — this guard checked nothing")
	}

	found := map[roundTripSite]bool{}
	routed := map[roundTripSite]bool{}

	for _, path := range files {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("could not parse %s: %v", path, err)
		}
		base := filepath.Base(path)
		for _, decl := range parsed.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Body == nil {
				continue
			}
			site := roundTripSite{file: base, fn: fn.Name.Name}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				switch f := call.Fun.(type) {
				case *ast.SelectorExpr:
					// Any x.Do(...) where x names the client's own HTTP
					// client. Written as a suffix match so a rename of the
					// field still lands here rather than silently escaping.
					if f.Sel.Name == "Do" && namesHTTPClient(f.X) {
						found[site] = true
					}
				case *ast.Ident:
					if f.Name == "unreachableCallError" {
						routed[site] = true
					}
				}
				return true
			})
		}
	}

	want := map[roundTripSite]bool{}
	for _, s := range wantRoundTripSites {
		want[s] = true
	}

	var unlisted, stale []string
	for s := range found {
		if !want[s] {
			unlisted = append(unlisted, s.String())
		}
	}
	for s := range want {
		if !found[s] {
			stale = append(stale, s.String())
		}
	}
	sort.Strings(unlisted)
	sort.Strings(stale)

	if len(unlisted) > 0 {
		t.Errorf("these make an HTTP round trip and are NOT on wantRoundTripSites: %v\n\nA new one is how the ArgoCD server address gets back into an error. Add it to the list and make it call unreachableCallError.", unlisted)
	}
	if len(stale) > 0 {
		t.Errorf("these are on wantRoundTripSites but no longer make a round trip: %v\n\nA stale entry means the list stopped describing the package, and the count below stopped meaning anything.", stale)
	}
	// Exact, with !=. A floor with room in it is a hole.
	if len(found) != len(wantRoundTripSites) {
		t.Errorf("the walk found %d round-trip sites; the list has %d entries", len(found), len(wantRoundTripSites))
	}

	for _, s := range wantRoundTripSites {
		if !routed[s] {
			t.Errorf("%s makes an HTTP round trip but never calls unreachableCallError. Its failure branch is free to wrap the transport error, and a wrapped transport error's words ARE the address Sharko dialled.", s)
		}
	}
}

// TestNoErrorInThisPackageIsBuiltFromTheBaseAddress is the other half. The
// round-trip guard says the failure branch is routed; this one says the base
// address is not read anywhere it could be turned into a message.
//
// The base address may be read in exactly two kinds of place: where the
// request URL is built, and where the client is constructed. Anywhere else is
// a new way for it to reach a person.
//
// NewClient and NewInClusterClient are deliberately absent: they SET the field
// in a composite literal, which is not a read, and the walk below does not see
// a key. probeArgoCD is absent for a different reason — it takes a discovered
// address as a parameter rather than reading the field, and it returns a bool,
// so nothing of what it dialled can escape it.
var wantBaseURLReaders = []roundTripSite{
	{file: "client.go", fn: "doGet"},
	{file: "client_write.go", fn: "doDelete"},
	{file: "client_write.go", fn: "doPost"},
	{file: "client_write.go", fn: "doPut"},
}

func TestNoErrorInThisPackageIsBuiltFromTheBaseAddress(t *testing.T) {
	files := shippingGoFiles(t, ".")
	if len(files) == 0 {
		t.Fatal("the walk read no Go files at all — this guard checked nothing")
	}

	found := map[roundTripSite]bool{}
	for _, path := range files {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("could not parse %s: %v", path, err)
		}
		base := filepath.Base(path)
		for _, decl := range parsed.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, isSel := n.(*ast.SelectorExpr)
				if isSel && sel.Sel.Name == "baseURL" {
					found[roundTripSite{file: base, fn: fn.Name.Name}] = true
				}
				return true
			})
		}
		// The struct field's own declaration is not a read; it lives outside
		// any function body and the walk above never sees it.
	}

	want := map[roundTripSite]bool{}
	for _, s := range wantBaseURLReaders {
		want[s] = true
	}
	var unlisted, stale []string
	for s := range found {
		if !want[s] {
			unlisted = append(unlisted, s.String())
		}
	}
	for s := range want {
		if !found[s] {
			stale = append(stale, s.String())
		}
	}
	sort.Strings(unlisted)
	sort.Strings(stale)

	if len(unlisted) > 0 {
		t.Errorf("these read the ArgoCD base address and are NOT on wantBaseURLReaders: %v\n\nThe base address is the one part of the request an operator can write a credential into. Reading it anywhere but where the request URL is built is how it reaches a message.", unlisted)
	}
	if len(stale) > 0 {
		t.Errorf("these are on wantBaseURLReaders but no longer read the base address: %v", stale)
	}
	if len(found) != len(wantBaseURLReaders) {
		t.Errorf("the walk found %d readers of the base address; the list has %d entries", len(found), len(wantBaseURLReaders))
	}
}

// shippingGoFiles lists the non-test .go files in dir. It walks the tree
// rather than naming files, so a file added tomorrow is read without anybody
// remembering this test exists.
func shippingGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("could not read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out
}

// namesHTTPClient reports whether an expression names the client's own HTTP
// client — c.httpClient, or a local holding it. Written as a name check rather
// than a type check so a round trip added on a differently-shaped receiver
// still lands in the guard instead of slipping past it.
func namesHTTPClient(x ast.Expr) bool {
	switch e := x.(type) {
	case *ast.SelectorExpr:
		return strings.Contains(e.Sel.Name, "httpClient") || strings.Contains(e.Sel.Name, "HTTPClient")
	case *ast.Ident:
		return strings.Contains(e.Name, "httpClient") || strings.Contains(e.Name, "client")
	}
	return false
}
