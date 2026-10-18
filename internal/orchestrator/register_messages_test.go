package orchestrator

// register_messages_test.go — the message of a registration that stopped
// part-way says exactly what happened (LO-9 R8). Every case runs the real
// RegisterCluster and pins the whole message by exact text.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// oldRegisterPartialWords are pieces of the old fixed messages. They were not
// true on every run ("ArgoCD registration not started" after the ArgoCD
// connection was saved, "Secrets created" when none were made), so no
// message may carry them again.
var oldRegisterPartialWords = []string{
	"ArgoCD registration not started",
	"ArgoCD registration and PR not started",
	"Secrets created",
	"Merge manually then ArgoCD registration will be needed",
	"Manual Git commit required",
	"Registration completed but",
	"addon secret(s)",
	"Try again once Git is reachable. Try again", // no doubled ending
}

func assertRegisterMessage(t *testing.T, res *RegisterClusterResult, wantStep, want string) {
	t.Helper()
	if res == nil {
		t.Fatal("no result")
	}
	if res.Status != "partial" {
		t.Errorf("status = %q, want partial", res.Status)
	}
	if res.FailedStep != wantStep {
		t.Errorf("failed_step = %q, want %q", res.FailedStep, wantStep)
	}
	if res.Message != want {
		t.Errorf("message:\n got: %q\nwant: %q", res.Message, want)
	}
	for _, old := range oldRegisterPartialWords {
		if strings.Contains(res.Message, old) {
			t.Errorf("message still has the old untrue words %q: %q", old, res.Message)
		}
	}
}

// failingClientFactory lets the first call through (the Stage 1 check uses
// it) and fails every later one (the addon secrets step).
func failingClientFactory() RemoteClientFactory {
	calls := 0
	return func(_ []byte) (kubernetes.Interface, error) {
		calls++
		if calls == 1 {
			return fake.NewSimpleClientset(), nil
		}
		return nil, errors.New("dial tcp: connection refused")
	}
}

func registerProdEU(t *testing.T, o *Orchestrator) *RegisterClusterResult {
	t.Helper()
	res, err := o.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"datadog": true},
		Region: "eu-west-1",
	})
	if err != nil {
		t.Fatalf("RegisterCluster: %v", err)
	}
	return res
}

func registerInline(t *testing.T, o *Orchestrator) *RegisterClusterResult {
	t.Helper()
	res, err := o.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-sharko",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
		Addons:     map[string]bool{"datadog": true},
	})
	if err != nil {
		t.Fatalf("RegisterCluster: %v", err)
	}
	return res
}

const argoSavedSentence = "Sharko had already saved the cluster's connection in ArgoCD. If the cluster is not in the cluster registry in Git within about 10 minutes, Sharko removes that connection again."

func TestRegisterPartialMessage_ConnectFailed(t *testing.T) {
	git := newMockGitProvider()
	o := New(nil, defaultCredsWithRaw(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	o.SetSecretManagement(defaultSecretDefs(), defaultSecretFetcher(), failingClientFactory())

	res := registerProdEU(t, o)
	assertRegisterMessage(t, res, "create_secrets",
		"Sharko could not connect to the cluster to create its addon secrets, so it stopped before opening a pull request. No addon secrets were created and nothing was committed to Git. Check that Sharko can reach the cluster, then try again.")
	if len(git.prs) != 0 {
		t.Errorf("a pull request was opened: %d", len(git.prs))
	}
}

func TestRegisterPartialMessage_ConnectFailed_ArgoConnectionSaved(t *testing.T) {
	asm := newMockArgoSecretManager()
	o := New(nil, nil, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	o.SetArgoSecretManager(asm, "")
	o.SetSecretManagement(defaultSecretDefs(), defaultSecretFetcher(), failingClientFactory())

	res := registerInline(t, o)
	if len(asm.ensured) != 1 {
		t.Fatalf("the ArgoCD connection was not written first (%d writes); the test does not test the case", len(asm.ensured))
	}
	assertRegisterMessage(t, res, "create_secrets",
		"Sharko could not connect to the cluster to create its addon secrets, so it stopped before opening a pull request. No addon secrets were created and nothing was committed to Git. "+
			argoSavedSentence+
			" Check that Sharko can reach the cluster, then try again.")
}

func TestRegisterPartialMessage_RegistryReadFailed_SecretsCreated(t *testing.T) {
	git := &failReadGit{mockGitProvider: newMockGitProvider(), path: mcPath, err: errGitDown}
	o := New(nil, defaultCredsWithRaw(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	o.SetSecretManagement(defaultSecretDefs(), defaultSecretFetcher(), fakeClientFactory())

	res := registerProdEU(t, o)
	assertRegisterMessage(t, res, "read_cluster_registry",
		"Sharko could not read the cluster registry from Git, so it stopped before opening a pull request — writing one built from an empty file would have dropped every other cluster out of the registry. Nothing was committed. Sharko created 1 addon secret on the cluster. Try again once Git is reachable.")
}

func TestRegisterPartialMessage_RegistryReadFailed_NothingElseDone(t *testing.T) {
	git := &failReadGit{mockGitProvider: newMockGitProvider(), path: mcPath, err: errGitDown}
	o := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	res := registerProdEU(t, o)
	assertRegisterMessage(t, res, "read_cluster_registry",
		"Sharko could not read the cluster registry from Git, so it stopped before opening a pull request — writing one built from an empty file would have dropped every other cluster out of the registry. Nothing was committed. Try again once Git is reachable.")
}

func TestRegisterPartialMessage_CommitFailed_NoSecrets(t *testing.T) {
	git := newMockGitProvider()
	git.createErr = errors.New("git API error")
	o := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	res := registerProdEU(t, o)
	assertRegisterMessage(t, res, "git_commit",
		"Sharko could not open the pull request that registers this cluster. Try again once Git is reachable.")
}

func TestRegisterPartialMessage_CommitFailed_SecretsCreatedAndArgoSaved(t *testing.T) {
	git := newMockGitProvider()
	git.createErr = errors.New("git API error")
	asm := newMockArgoSecretManager()
	o := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	o.SetArgoSecretManager(asm, "")
	o.SetSecretManagement(defaultSecretDefs(), defaultSecretFetcher(), fakeClientFactory())

	res := registerInline(t, o)
	assertRegisterMessage(t, res, "git_commit",
		"Sharko could not open the pull request that registers this cluster. Sharko created 1 addon secret on the cluster. "+
			argoSavedSentence+
			" Try again once Git is reachable.")
}

func TestRegisterPartialMessage_MergeFailed_SecretFailed(t *testing.T) {
	git := newMockGitProvider()
	git.mergeErr = errors.New("merge conflict")
	o := New(nil, defaultCredsWithRaw(), newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	o.SetSecretManagement(defaultSecretDefs(), &mockSecretFetcher{err: errors.New("vault connection refused")}, fakeClientFactory())

	res := registerProdEU(t, o)
	if res.Git == nil || res.Git.PRUrl == "" {
		t.Fatalf("no pull request in the answer: %+v", res.Git)
	}
	assertRegisterMessage(t, res, "pr_merge",
		"Sharko opened the pull request that registers this cluster, but could not merge it. Merge it by hand to finish the registration. 1 addon secret could not be created on the cluster. Pull request: "+res.Git.PRUrl)
}

func TestRegisterPartialMessage_MergeFailed_NoSecretsAtAll(t *testing.T) {
	git := newMockGitProvider()
	git.mergeErr = errors.New("merge conflict")
	o := New(nil, defaultCreds(), newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	res := registerProdEU(t, o)
	if res.Git == nil || res.Git.PRUrl == "" {
		t.Fatalf("no pull request in the answer: %+v", res.Git)
	}
	assertRegisterMessage(t, res, "pr_merge",
		"Sharko opened the pull request that registers this cluster, but could not merge it. Merge it by hand to finish the registration. Pull request: "+res.Git.PRUrl)
}

func TestRegisterPartialMessage_MergeFailed_ArgoSaved(t *testing.T) {
	git := newMockGitProvider()
	git.mergeErr = errors.New("merge conflict")
	asm := newMockArgoSecretManager()
	o := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	o.SetArgoSecretManager(asm, "")

	res := registerInline(t, o)
	if res.Git == nil || res.Git.PRUrl == "" {
		t.Fatalf("no pull request in the answer: %+v", res.Git)
	}
	assertRegisterMessage(t, res, "pr_merge",
		"Sharko opened the pull request that registers this cluster, but could not merge it. Merge it by hand to finish the registration. "+
			argoSavedSentence+
			" Pull request: "+res.Git.PRUrl)
}

func TestRegisterPartialMessage_SecretsFailed_PROpen(t *testing.T) {
	o := New(nil, defaultCredsWithRaw(), newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	o.SetSecretManagement(defaultSecretDefs(), &mockSecretFetcher{err: errors.New("vault connection refused")}, fakeClientFactory())

	res := registerProdEU(t, o)
	assertRegisterMessage(t, res, "create_secrets",
		"Sharko opened the pull request that registers this cluster, but 1 addon secret could not be created on it. The registration finishes when the pull request is merged.")
}

func TestRegisterPartialMessage_SecretsFailed_PRMerged(t *testing.T) {
	o := New(nil, defaultCredsWithRaw(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	o.SetSecretManagement(defaultSecretDefs(), &mockSecretFetcher{err: errors.New("vault connection refused")}, fakeClientFactory())

	res := registerProdEU(t, o)
	if res.Git == nil || !res.Git.Merged {
		t.Fatalf("the pull request is not merged; the test does not test the case: %+v", res.Git)
	}
	assertRegisterMessage(t, res, "create_secrets",
		"The cluster is registered, but 1 addon secret could not be created on it.")
}

func TestAddonSecretCount(t *testing.T) {
	for n, want := range map[int]string{1: "1 addon secret", 2: "2 addon secrets", 0: "0 addon secrets"} {
		if got := addonSecretCount(n); got != want {
			t.Errorf("addonSecretCount(%d) = %q, want %q", n, got, want)
		}
	}
}
