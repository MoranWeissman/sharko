package orchestrator

// cluster_server_address_test.go — LO-9 R11. Registering a cluster refuses a
// cluster address with a user name, password, query or fragment in it, and
// the refusal never repeats those parts.

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

const (
	wantAddressRefusal    = "The cluster address in these credentials has a user name, password, query or fragment in it. Sharko does not register a cluster with an address like that, because a password or token can hide in those parts. Use the address without them: https://kube.example:6443"
	wantAddressUnreadable = "Sharko could not read the cluster address in these credentials, so it cannot check that there is no user name, password, query or fragment in it. Fix the address (for example https://my-cluster.example.com:6443) and try again."
)

// The parts of the bad addresses below that must never come back.
var addressSecretParts = []string{"alice", "s3cret", "tok3n", "frag-pw"}

var refusedServers = map[string]string{
	"user and password": "https://alice:s3cret@kube.example:6443",
	"user only":         "https://alice@kube.example:6443",
	"query":             "https://kube.example:6443?token=tok3n",
	"fragment":          "https://kube.example:6443#frag-pw",
}

func inlineKubeconfigWithServer(server string) string {
	return strings.Replace(v125TestBearerKubeconfig, "server: https://127.0.0.1:60123", "server: "+server, 1)
}

func assertAddressRefused(t *testing.T, res *RegisterClusterResult, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("the address was not refused: %+v", res)
	}
	if !IsClusterServerAddressRefused(err) {
		t.Errorf("error is not a ClusterServerAddressError (the API would not answer 400): %v", err)
	}
	if err.Error() != want {
		t.Errorf("refusal:\n got: %q\nwant: %q", err.Error(), want)
	}
	for _, part := range addressSecretParts {
		if strings.Contains(err.Error(), part) {
			t.Errorf("the refusal repeats %q: %q", part, err.Error())
		}
	}
}

func TestRegisterCluster_RefusesInlineServerWithCredential(t *testing.T) {
	for name, server := range refusedServers {
		t.Run(name, func(t *testing.T) {
			git := newMockGitProvider()
			asm := newMockArgoSecretManager()
			o := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
			o.SetArgoSecretManager(asm, "")
			o.SetSecretManagement(defaultSecretDefs(), defaultSecretFetcher(), fakeClientFactory())

			res, err := o.RegisterCluster(context.Background(), RegisterClusterRequest{
				Name:       "kind-sharko",
				Provider:   "kubeconfig",
				Kubeconfig: inlineKubeconfigWithServer(server),
				Addons:     map[string]bool{"datadog": true},
			})
			assertAddressRefused(t, res, err, wantAddressRefusal)
			if len(asm.ensured) != 0 {
				t.Errorf("the ArgoCD connection was written for a refused address (%d writes)", len(asm.ensured))
			}
			if len(git.prs) != 0 {
				t.Errorf("a pull request was opened for a refused address (%d)", len(git.prs))
			}
		})
	}
}

func TestRegisterCluster_RefusesBackendServerWithCredential(t *testing.T) {
	for name, server := range refusedServers {
		t.Run(name, func(t *testing.T) {
			git := newMockGitProvider()
			creds := &mockCredProvider{creds: map[string]*providers.Kubeconfig{
				"prod-eu": {Server: server, Token: "fake-token"},
			}}
			o := New(nil, creds, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

			res, err := o.RegisterCluster(context.Background(), RegisterClusterRequest{
				Name:   "prod-eu",
				Region: "eu-west-1",
			})
			assertAddressRefused(t, res, err, wantAddressRefusal)
			if len(git.prs) != 0 {
				t.Errorf("a pull request was opened for a refused address (%d)", len(git.prs))
			}
		})
	}
}

func TestRegisterCluster_RefusesUnreadableServer(t *testing.T) {
	creds := &mockCredProvider{creds: map[string]*providers.Kubeconfig{
		"prod-eu": {Server: "https://alice:s3cret@kube.example:99999", Token: "fake-token"},
	}}
	o := New(nil, creds, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	res, err := o.RegisterCluster(context.Background(), RegisterClusterRequest{Name: "prod-eu", Region: "eu-west-1"})
	assertAddressRefused(t, res, err, wantAddressUnreadable)
}

func TestRegisterCluster_PlainServerIsNotRefused(t *testing.T) {
	for _, server := range []string{"https://kube.example:6443", "https://kubernetes.default.svc", "https://10.0.0.1", "https://[::1]:6443/k8s/clusters/c-1"} {
		if err := checkClusterServerAddress(server); err != nil {
			t.Errorf("%s was refused: %v", server, err)
		}
	}
	if err := checkClusterServerAddress(""); err != nil {
		t.Errorf("an empty address is left to the code that handles it, got %v", err)
	}
}
