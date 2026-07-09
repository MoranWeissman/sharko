package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
)

// argoCDSecret is a small helper to build a typed cluster Secret for the fake client.
func argoCDSecret(name, displayName, server, configJSON string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "cluster",
				"app.kubernetes.io/managed-by":   "sharko",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(displayName),
			"server": []byte(server),
			"config": []byte(configJSON),
		},
	}
}

// argoCDSecretWithRegion is argoCDSecret plus a "region" label — the fallback
// region source the AWS-mint paths consult when the config JSON itself
// carries no region (V2-cleanup-88.2).
func argoCDSecretWithRegion(name, displayName, server, region, configJSON string) *corev1.Secret {
	s := argoCDSecret(name, displayName, server, configJSON)
	s.Labels["region"] = region
	return s
}

// fakeCAB64 is a deterministic base64 string used by tests that need a non-empty
// caData payload. The kubeconfig spec doesn't validate the decoded payload at
// parse time, so an arbitrary base64 string is sufficient for the round-trip
// assertion.
var fakeCAB64 = base64.StdEncoding.EncodeToString([]byte("fake-ca-data"))

// TestArgoCD_BearerToken_HappyPath covers shape 1: bearerToken + caData.
// Asserts Server/Token/CAData are populated and Raw round-trips through
// clientcmd.RESTConfigFromKubeConfig.
func TestArgoCD_BearerToken_HappyPath(t *testing.T) {
	configJSON := `{
		"bearerToken": "eyJhbGciOi-fake-token",
		"tlsClientConfig": {
			"caData": "` + fakeCAB64 + `",
			"insecure": false
		}
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"prod-eu", "prod-eu",
		"https://api.cluster-1.example.com:6443",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	kc, err := provider.GetCredentials("prod-eu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if kc.Server != "https://api.cluster-1.example.com:6443" {
		t.Errorf("Server = %q, want %q", kc.Server, "https://api.cluster-1.example.com:6443")
	}
	if kc.Token != "eyJhbGciOi-fake-token" {
		t.Errorf("Token = %q, want %q", kc.Token, "eyJhbGciOi-fake-token")
	}
	if string(kc.CAData) != "fake-ca-data" {
		t.Errorf("CAData = %q, want %q (base64-decoded)", string(kc.CAData), "fake-ca-data")
	}
	if len(kc.Raw) == 0 {
		t.Fatal("Raw kubeconfig is empty")
	}

	// Round-trip check: synthesized kubeconfig must parse cleanly via clientcmd.
	parsed, err := clientcmd.RESTConfigFromKubeConfig(kc.Raw)
	if err != nil {
		t.Fatalf("synthesized Raw kubeconfig failed clientcmd round-trip: %v", err)
	}
	if parsed.Host != "https://api.cluster-1.example.com:6443" {
		t.Errorf("round-trip Host = %q, want %q", parsed.Host, "https://api.cluster-1.example.com:6443")
	}
	if parsed.BearerToken != "eyJhbGciOi-fake-token" {
		t.Errorf("round-trip BearerToken = %q, want %q", parsed.BearerToken, "eyJhbGciOi-fake-token")
	}
}

// TestArgoCD_BearerToken_Insecure covers shape 1 with insecure:true and no caData.
// Asserts CAData is empty and Raw still round-trips.
func TestArgoCD_BearerToken_Insecure(t *testing.T) {
	configJSON := `{
		"bearerToken": "insecure-token",
		"tlsClientConfig": { "insecure": true }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"local-kind", "local-kind",
		"https://127.0.0.1:6443",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	kc, err := provider.GetCredentials("local-kind")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(kc.CAData) != 0 {
		t.Errorf("CAData should be empty for insecure cluster, got %d bytes", len(kc.CAData))
	}
	if kc.Token != "insecure-token" {
		t.Errorf("Token = %q, want %q", kc.Token, "insecure-token")
	}

	// Round-trip — insecure clusters must still parse.
	if _, err := clientcmd.RESTConfigFromKubeConfig(kc.Raw); err != nil {
		t.Fatalf("synthesized insecure kubeconfig failed clientcmd round-trip: %v", err)
	}
}

// TestArgoCD_AWSAuthConfig_ReturnsIAMRequired covers the awsAuthConfig shape
// when no region is resolvable anywhere (no region field, no "region" label
// on the Secret) — Sharko can parse the shape but cannot mint, so it returns
// the typed IAMRequired error naming region as the missing piece
// (V2-cleanup-88.2). Asserts errors.Is(ErrArgoCDProviderIAMRequired) and the
// stable Code — both preserved from the pre-88.2 "always unsupported"
// behavior, now repurposed to mean "parsed but couldn't mint" rather than
// "never attempted".
func TestArgoCD_AWSAuthConfig_ReturnsIAMRequired(t *testing.T) {
	configJSON := `{
		"awsAuthConfig": {
			"clusterName": "my-eks-cluster",
			"roleARN": "arn:aws:iam::123456789012:role/EKSReadRole"
		},
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"prod-eks", "prod-eks",
		"https://abc.eks.amazonaws.com",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("prod-eks")
	if err == nil {
		t.Fatal("expected error for awsAuthConfig shape with no resolvable region, got nil")
	}
	if !errors.Is(err, ErrArgoCDProviderIAMRequired) {
		t.Errorf("errors.Is(err, ErrArgoCDProviderIAMRequired) = false, error: %v", err)
	}

	var pe *ArgoCDProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error is not *ArgoCDProviderError: %T", err)
	}
	if pe.Code != ArgoCDProviderCodeIAMRequired {
		t.Errorf("Code = %q, want %q", pe.Code, ArgoCDProviderCodeIAMRequired)
	}
	if pe.ClusterName != "prod-eks" {
		t.Errorf("ClusterName = %q, want %q", pe.ClusterName, "prod-eks")
	}
	if pe.Server != "https://abc.eks.amazonaws.com" {
		t.Errorf("Server = %q, want %q", pe.Server, "https://abc.eks.amazonaws.com")
	}
	if !strings.Contains(pe.Detail, "region") {
		t.Errorf("Detail = %q, want it to name \"region\" as the missing piece", pe.Detail)
	}
}

// TestArgoCD_AWSAuthConfig_MintsWithOwnIdentity is the V2-cleanup-88.2 happy
// path: awsAuthConfig parses, a region is resolvable via the Secret's
// "region" label, and the stubbed eksTokenFn mints successfully. Asserts the
// resulting Kubeconfig carries the minted token/server/CAData AND that
// eksTokenFn was invoked with the parsed clusterName/region/roleARN — proving
// Sharko mints with ITS OWN identity assuming the SAME role, never executing
// anything.
func TestArgoCD_AWSAuthConfig_MintsWithOwnIdentity(t *testing.T) {
	configJSON := `{
		"awsAuthConfig": {
			"clusterName": "my-eks-cluster",
			"roleARN": "arn:aws:iam::123456789012:role/example"
		},
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecretWithRegion(
		"prod-eks", "prod-eks",
		"https://abc.eks.amazonaws.com", "us-east-1",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	var gotClusterName, gotRegion, gotRoleARN string
	provider.eksTokenFn = func(_ context.Context, clusterName, region, roleARN string) (string, error) {
		gotClusterName, gotRegion, gotRoleARN = clusterName, region, roleARN
		return "k8s-aws-v1.minted-token", nil
	}

	kc, err := provider.GetCredentials("prod-eks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotClusterName != "my-eks-cluster" {
		t.Errorf("eksTokenFn clusterName = %q, want %q", gotClusterName, "my-eks-cluster")
	}
	if gotRegion != "us-east-1" {
		t.Errorf("eksTokenFn region = %q, want %q", gotRegion, "us-east-1")
	}
	if gotRoleARN != "arn:aws:iam::123456789012:role/example" {
		t.Errorf("eksTokenFn roleARN = %q, want %q", gotRoleARN, "arn:aws:iam::123456789012:role/example")
	}

	if kc.Token != "k8s-aws-v1.minted-token" {
		t.Errorf("Token = %q, want the minted token", kc.Token)
	}
	if kc.Server != "https://abc.eks.amazonaws.com" {
		t.Errorf("Server = %q, want %q", kc.Server, "https://abc.eks.amazonaws.com")
	}
	if string(kc.CAData) != "fake-ca-data" {
		t.Errorf("CAData = %q, want %q (base64-decoded)", string(kc.CAData), "fake-ca-data")
	}
	if _, err := clientcmd.RESTConfigFromKubeConfig(kc.Raw); err != nil {
		t.Fatalf("synthesized Raw kubeconfig failed clientcmd round-trip: %v", err)
	}
}

// TestArgoCD_AWSAuthConfig_ClusterNameFallsBackToDisplayName covers the
// defensive-parse rule: when awsAuthConfig.clusterName is empty (a
// hand-edited or unusual Secret), Sharko falls back to the Secret's own
// display name rather than failing.
func TestArgoCD_AWSAuthConfig_ClusterNameFallsBackToDisplayName(t *testing.T) {
	configJSON := `{
		"awsAuthConfig": { "roleARN": "arn:aws:iam::123456789012:role/example" },
		"tlsClientConfig": { "insecure": true }
	}`

	client := fake.NewSimpleClientset(argoCDSecretWithRegion(
		"prod-eks", "prod-eks", "https://abc.eks.amazonaws.com", "eu-west-1", configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	var gotClusterName string
	provider.eksTokenFn = func(_ context.Context, clusterName, _, _ string) (string, error) {
		gotClusterName = clusterName
		return "tok", nil
	}

	if _, err := provider.GetCredentials("prod-eks"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotClusterName != "prod-eks" {
		t.Errorf("eksTokenFn clusterName = %q, want fallback to display name %q", gotClusterName, "prod-eks")
	}
}

// TestArgoCD_AWSAuthConfig_MintFails_ReturnsIAMRequired covers a resolvable
// region but a failing mint (no IRSA/Pod Identity on the Sharko pod, STS
// denies AssumeRole, etc.) — still the typed IAMRequired error, but the
// Detail explains the AWS-identity gap rather than a missing region.
func TestArgoCD_AWSAuthConfig_MintFails_ReturnsIAMRequired(t *testing.T) {
	configJSON := `{
		"awsAuthConfig": { "clusterName": "my-eks-cluster" },
		"tlsClientConfig": { "insecure": true }
	}`

	client := fake.NewSimpleClientset(argoCDSecretWithRegion(
		"prod-eks", "prod-eks", "https://abc.eks.amazonaws.com", "us-east-1", configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")
	provider.eksTokenFn = func(_ context.Context, _, _, _ string) (string, error) {
		return "", errors.New("no valid AWS credential sources found")
	}

	_, err := provider.GetCredentials("prod-eks")
	if err == nil {
		t.Fatal("expected error when mint fails, got nil")
	}
	if !errors.Is(err, ErrArgoCDProviderIAMRequired) {
		t.Errorf("errors.Is(err, ErrArgoCDProviderIAMRequired) = false, error: %v", err)
	}
	var pe *ArgoCDProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error is not *ArgoCDProviderError: %T", err)
	}
	if pe.Code != ArgoCDProviderCodeIAMRequired {
		t.Errorf("Code = %q, want %q", pe.Code, ArgoCDProviderCodeIAMRequired)
	}
	if !strings.Contains(pe.Detail, "AWS identity") {
		t.Errorf("Detail = %q, want it to mention Sharko needing its own AWS identity", pe.Detail)
	}
}

// TestArgoCD_ExecProviderConfig_UnknownCommand_ReturnsExecUnsupported covers
// an exec-plugin command Sharko does not recognize as an AWS authenticator
// (e.g. a GCP/Azure helper). Asserts errors.Is(ErrArgoCDProviderExecUnsupported)
// and the stable Code — unrecognized commands are still rejected outright,
// exactly as before V2-cleanup-88.2.
func TestArgoCD_ExecProviderConfig_UnknownCommand_ReturnsExecUnsupported(t *testing.T) {
	configJSON := `{
		"execProviderConfig": {
			"command": "gke-gcloud-auth-plugin",
			"apiVersion": "client.authentication.k8s.io/v1beta1"
		},
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"exec-cluster", "exec-cluster",
		"https://exec.example.com",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("exec-cluster")
	if err == nil {
		t.Fatal("expected error for unrecognized execProviderConfig command, got nil")
	}
	if !errors.Is(err, ErrArgoCDProviderExecUnsupported) {
		t.Errorf("errors.Is(err, ErrArgoCDProviderExecUnsupported) = false, error: %v", err)
	}

	var pe *ArgoCDProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error is not *ArgoCDProviderError: %T", err)
	}
	if pe.Code != ArgoCDProviderCodeExecUnsupported {
		t.Errorf("Code = %q, want %q", pe.Code, ArgoCDProviderCodeExecUnsupported)
	}
}

// TestArgoCD_ExecProviderConfig_ArgoCDK8sAuthAWS_NonAWSSubcommand_ReturnsExecUnsupported
// covers command "argocd-k8s-auth" with a NON-"aws" first arg (e.g. "gcp") —
// the command name alone isn't enough; the subcommand must be "aws" too.
func TestArgoCD_ExecProviderConfig_ArgoCDK8sAuthNonAWSSubcommand_ReturnsExecUnsupported(t *testing.T) {
	configJSON := `{
		"execProviderConfig": {
			"command": "argocd-k8s-auth",
			"args": ["gcp"],
			"apiVersion": "client.authentication.k8s.io/v1beta1"
		},
		"tlsClientConfig": { "insecure": true }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"gcp-cluster", "gcp-cluster", "https://gcp.example.com", configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("gcp-cluster")
	if !errors.Is(err, ErrArgoCDProviderExecUnsupported) {
		t.Errorf("errors.Is(err, ErrArgoCDProviderExecUnsupported) = false, error: %v", err)
	}
}

// TestArgoCD_ExecProviderConfig_ArgoCDK8sAuthAWS_MintsWithOwnIdentity covers
// the real-world shape from the design doc: command argocd-k8s-auth, args
// ["aws", "--cluster-name", "<name>", "--role-arn", "<arn>"], env
// AWS_REGION=<region>. Asserts the parsed values are forwarded to eksTokenFn
// verbatim and the resulting Kubeconfig carries the minted token.
func TestArgoCD_ExecProviderConfig_ArgoCDK8sAuthAWS_MintsWithOwnIdentity(t *testing.T) {
	configJSON := `{
		"execProviderConfig": {
			"command": "argocd-k8s-auth",
			"args": ["aws", "--cluster-name", "my-eks-cluster", "--role-arn", "arn:aws:iam::123456789012:role/example"],
			"env": { "AWS_REGION": "us-west-2" },
			"apiVersion": "client.authentication.k8s.io/v1beta1"
		},
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"prod-eks", "prod-eks", "https://exec-eks.example.com", configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	var gotClusterName, gotRegion, gotRoleARN string
	provider.eksTokenFn = func(_ context.Context, clusterName, region, roleARN string) (string, error) {
		gotClusterName, gotRegion, gotRoleARN = clusterName, region, roleARN
		return "minted-exec-token", nil
	}

	kc, err := provider.GetCredentials("prod-eks")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotClusterName != "my-eks-cluster" {
		t.Errorf("eksTokenFn clusterName = %q, want %q", gotClusterName, "my-eks-cluster")
	}
	if gotRegion != "us-west-2" {
		t.Errorf("eksTokenFn region = %q, want %q", gotRegion, "us-west-2")
	}
	if gotRoleARN != "arn:aws:iam::123456789012:role/example" {
		t.Errorf("eksTokenFn roleARN = %q, want %q", gotRoleARN, "arn:aws:iam::123456789012:role/example")
	}
	if kc.Token != "minted-exec-token" {
		t.Errorf("Token = %q, want the minted token", kc.Token)
	}
}

// TestArgoCD_ExecProviderConfig_AWSIAMAuthenticator_MintsWithOwnIdentity
// covers the standalone aws-iam-authenticator command (no "aws" subcommand
// needed — the command name alone is the AWS signal).
func TestArgoCD_ExecProviderConfig_AWSIAMAuthenticator_MintsWithOwnIdentity(t *testing.T) {
	configJSON := `{
		"execProviderConfig": {
			"command": "aws-iam-authenticator",
			"args": ["--cluster-name", "my-eks-cluster"],
			"env": { "AWS_REGION": "ap-south-1" },
			"apiVersion": "client.authentication.k8s.io/v1beta1"
		},
		"tlsClientConfig": { "insecure": true }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"prod-eks-2", "prod-eks-2", "https://exec-eks-2.example.com", configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	var gotClusterName, gotRegion, gotRoleARN string
	provider.eksTokenFn = func(_ context.Context, clusterName, region, roleARN string) (string, error) {
		gotClusterName, gotRegion, gotRoleARN = clusterName, region, roleARN
		return "minted-token-2", nil
	}

	if _, err := provider.GetCredentials("prod-eks-2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotClusterName != "my-eks-cluster" {
		t.Errorf("eksTokenFn clusterName = %q, want %q", gotClusterName, "my-eks-cluster")
	}
	if gotRegion != "ap-south-1" {
		t.Errorf("eksTokenFn region = %q, want %q", gotRegion, "ap-south-1")
	}
	if gotRoleARN != "" {
		t.Errorf("eksTokenFn roleARN = %q, want empty (no --role-arn in args)", gotRoleARN)
	}
}

// TestArgoCD_ExecProviderConfig_MissingRoleARN_MintsWithBaseIdentity is the
// explicit "role optional" constraint: no --role-arn anywhere → Sharko still
// mints, passing an empty roleARN through to eksTokenFn (which mints with
// Sharko's base identity, no AssumeRole hop).
func TestArgoCD_ExecProviderConfig_MissingRoleARN_MintsWithBaseIdentity(t *testing.T) {
	configJSON := `{
		"execProviderConfig": {
			"command": "argocd-k8s-auth",
			"args": ["aws", "--cluster-name", "my-eks-cluster"],
			"env": { "AWS_REGION": "us-east-2" }
		},
		"tlsClientConfig": { "insecure": true }
	}`
	client := fake.NewSimpleClientset(argoCDSecret(
		"no-role-cluster", "no-role-cluster", "https://exec-eks-3.example.com", configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	var gotRoleARN string
	var called bool
	provider.eksTokenFn = func(_ context.Context, _, _, roleARN string) (string, error) {
		called = true
		gotRoleARN = roleARN
		return "tok", nil
	}

	if _, err := provider.GetCredentials("no-role-cluster"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected eksTokenFn to be called (role is optional, not a hard requirement)")
	}
	if gotRoleARN != "" {
		t.Errorf("eksTokenFn roleARN = %q, want empty", gotRoleARN)
	}
}

// TestArgoCD_ExecProviderConfig_MalformedArgs_FallsBackGracefully covers
// defensive parsing: a trailing "--cluster-name" flag with no value must not
// panic and must fall back to the Secret's display name.
func TestArgoCD_ExecProviderConfig_MalformedArgs_FallsBackGracefully(t *testing.T) {
	configJSON := `{
		"execProviderConfig": {
			"command": "argocd-k8s-auth",
			"args": ["aws", "--cluster-name"],
			"env": { "AWS_REGION": "us-east-1" }
		},
		"tlsClientConfig": { "insecure": true }
	}`
	client := fake.NewSimpleClientset(argoCDSecret(
		"malformed-cluster", "malformed-cluster", "https://exec-eks-4.example.com", configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	var gotClusterName string
	provider.eksTokenFn = func(_ context.Context, clusterName, _, _ string) (string, error) {
		gotClusterName = clusterName
		return "tok", nil
	}

	if _, err := provider.GetCredentials("malformed-cluster"); err != nil {
		t.Fatalf("unexpected error (malformed args must not panic or hard-fail): %v", err)
	}
	if gotClusterName != "malformed-cluster" {
		t.Errorf("eksTokenFn clusterName = %q, want fallback to display name %q", gotClusterName, "malformed-cluster")
	}
}

// TestArgoCD_ExecProviderConfig_MissingRegion_ReturnsIAMRequired covers a
// known AWS command with no AWS_REGION env and no region label — the typed
// error names region as the missing piece, mirroring the awsAuthConfig case.
func TestArgoCD_ExecProviderConfig_MissingRegion_ReturnsIAMRequired(t *testing.T) {
	configJSON := `{
		"execProviderConfig": {
			"command": "aws-iam-authenticator",
			"args": ["--cluster-name", "my-eks-cluster"]
		},
		"tlsClientConfig": { "insecure": true }
	}`
	client := fake.NewSimpleClientset(argoCDSecret(
		"no-region-cluster", "no-region-cluster", "https://exec-eks-5.example.com", configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("no-region-cluster")
	if !errors.Is(err, ErrArgoCDProviderIAMRequired) {
		t.Errorf("errors.Is(err, ErrArgoCDProviderIAMRequired) = false, error: %v", err)
	}
	var pe *ArgoCDProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error is not *ArgoCDProviderError: %T", err)
	}
	if !strings.Contains(pe.Detail, "region") {
		t.Errorf("Detail = %q, want it to name \"region\" as the missing piece", pe.Detail)
	}
}

// TestArgoCD_AWSAuthConfig_RegionFromServerURL_MintsWithOwnIdentity covers
// the last-resort region fallback: no region field, no "region" label, but
// the server is a real-shaped EKS API endpoint
// (<id>.gr7.<region>.eks.amazonaws.com) — Sharko parses the region straight
// out of the hostname and mints successfully.
func TestArgoCD_AWSAuthConfig_RegionFromServerURL_MintsWithOwnIdentity(t *testing.T) {
	configJSON := `{
		"awsAuthConfig": { "clusterName": "my-eks-cluster" },
		"tlsClientConfig": { "insecure": true }
	}`
	client := fake.NewSimpleClientset(argoCDSecret(
		"prod-eks", "prod-eks", "https://ABC123.gr7.eu-west-1.eks.amazonaws.com", configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	var gotRegion string
	provider.eksTokenFn = func(_ context.Context, _, region, _ string) (string, error) {
		gotRegion = region
		return "tok", nil
	}

	if _, err := provider.GetCredentials("prod-eks"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotRegion != "eu-west-1" {
		t.Errorf("eksTokenFn region = %q, want %q (parsed from server URL)", gotRegion, "eu-west-1")
	}
}

// TestArgoCD_ExecProviderConfig_MatchesSharkoOwnWriterShape_MintsViaServerURLRegion
// is the critical regression-covering test for V2-cleanup-88.2: it reproduces
// EXACTLY what Sharko's own writer (argosecrets.buildSecretConfig, the exec
// branch) emits for its own EKS cluster registrations — command
// "argocd-k8s-auth", args ["aws", "--cluster-name", <name>, "--role-arn",
// <arn>], and deliberately NO env field (buildSecretConfig's doc comment:
// "No env vars are set: ... ArgoCD v2.14 cannot unmarshal the env field").
// No "region" label either (buildLabels never sets one). Without the
// server-URL-derived region fallback, EVERY Sharko-registered EKS cluster
// would hit the missing-region error and this story would do nothing for
// the primary real-world case. This test proves it mints instead.
func TestArgoCD_ExecProviderConfig_MatchesSharkoOwnWriterShape_MintsViaServerURLRegion(t *testing.T) {
	configJSON := `{
		"execProviderConfig": {
			"command": "argocd-k8s-auth",
			"args": ["aws", "--cluster-name", "prod-eks", "--role-arn", "arn:aws:iam::123456789012:role/example"],
			"apiVersion": "client.authentication.k8s.io/v1beta1"
		},
		"tlsClientConfig": { "insecure": false, "caData": "` + fakeCAB64 + `" }
	}`
	client := fake.NewSimpleClientset(argoCDSecret(
		"prod-eks", "prod-eks", "https://XYZ789.gr7.ap-southeast-2.eks.amazonaws.com", configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	var gotClusterName, gotRegion, gotRoleARN string
	provider.eksTokenFn = func(_ context.Context, clusterName, region, roleARN string) (string, error) {
		gotClusterName, gotRegion, gotRoleARN = clusterName, region, roleARN
		return "minted-for-sharko-own-cluster", nil
	}

	kc, err := provider.GetCredentials("prod-eks")
	if err != nil {
		t.Fatalf("unexpected error (this shape is exactly what Sharko's own writer emits — it must mint): %v", err)
	}
	if gotClusterName != "prod-eks" {
		t.Errorf("eksTokenFn clusterName = %q, want %q", gotClusterName, "prod-eks")
	}
	if gotRegion != "ap-southeast-2" {
		t.Errorf("eksTokenFn region = %q, want %q (parsed from server URL, since Sharko's own writer sets neither env nor a label)", gotRegion, "ap-southeast-2")
	}
	if gotRoleARN != "arn:aws:iam::123456789012:role/example" {
		t.Errorf("eksTokenFn roleARN = %q, want %q", gotRoleARN, "arn:aws:iam::123456789012:role/example")
	}
	if kc.Token != "minted-for-sharko-own-cluster" {
		t.Errorf("Token = %q, want the minted token", kc.Token)
	}
}

// TestRegionFromEKSServerURL is a table-driven unit test of the pure
// hostname-parsing helper, independent of the GetCredentials integration
// tests above.
func TestRegionFromEKSServerURL(t *testing.T) {
	tests := []struct {
		name   string
		server string
		want   string
	}{
		{"standard_gr7_shape", "https://ABC123.gr7.us-east-1.eks.amazonaws.com", "us-east-1"},
		{"multi_segment_region", "https://ABC123.gr7.ap-southeast-2.eks.amazonaws.com", "ap-southeast-2"},
		{"older_shape_no_gr7", "https://ABC123.us-west-2.eks.amazonaws.com", "us-west-2"},
		{"with_port", "https://ABC123.gr7.eu-central-1.eks.amazonaws.com:443", "eu-central-1"},
		{"no_region_segment", "https://abc.eks.amazonaws.com", ""},
		{"non_eks_host", "https://api.cluster-1.example.com:6443", ""},
		{"non_eks_aws_host", "https://s3.amazonaws.com", ""},
		{"empty_string", "", ""},
		{"malformed_url", "://not a url", ""},
		{"ip_literal_server", "https://10.0.0.1:6443", ""},
		{"govcloud_different_suffix_unmatched", "https://ABC123.gr7.us-gov-west-1.eks.amazonaws-us-gov.com", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := regionFromEKSServerURL(tt.server); got != tt.want {
				t.Errorf("regionFromEKSServerURL(%q) = %q, want %q", tt.server, got, tt.want)
			}
		})
	}
}

// TestArgoCD_CertAndAWSAuthBothPresent_CertWins pins the reuse-first
// ordering (V2-cleanup-88.2 L5): when a Secret carries BOTH a client
// certificate AND an awsAuthConfig (never written by ArgoCD/Sharko in
// practice, but defends a hand-edited Secret), the Secret's own material
// (the certificate) wins — Sharko never attempts to mint when it already has
// usable credentials in hand. eksTokenFn is deliberately left nil; a test
// failure here (nil-pointer panic) would prove the ordering broke.
func TestArgoCD_CertAndAWSAuthBothPresent_CertWins(t *testing.T) {
	certB64 := base64.StdEncoding.EncodeToString([]byte("fake-cert-data"))
	keyB64 := base64.StdEncoding.EncodeToString([]byte("fake-key-data"))
	configJSON := `{
		"awsAuthConfig": { "clusterName": "my-eks-cluster" },
		"tlsClientConfig": {
			"caData": "` + fakeCAB64 + `",
			"certData": "` + certB64 + `",
			"keyData": "` + keyB64 + `"
		}
	}`

	client := fake.NewSimpleClientset(argoCDSecretWithRegion(
		"both-shapes", "both-shapes", "https://both.example.com", "us-east-1", configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")
	// eksTokenFn intentionally left nil — if the cert branch didn't win,
	// calling it would panic, failing this test loudly.

	kc, err := provider.GetCredentials("both-shapes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(kc.CertData) == 0 || len(kc.KeyData) == 0 {
		t.Fatal("expected cert-shape Kubeconfig (CertData/KeyData set), got bearer/token shape")
	}
	if kc.Token != "" {
		t.Errorf("Token = %q, want empty — cert shape must not carry a minted token", kc.Token)
	}
}

// TestArgoCD_UnknownAuthShape_ReturnsUnsupportedAuth covers a parseable but
// empty config (no bearerToken, no awsAuthConfig, no execProviderConfig).
func TestArgoCD_UnknownAuthShape_ReturnsUnsupportedAuth(t *testing.T) {
	configJSON := `{
		"tlsClientConfig": { "caData": "` + fakeCAB64 + `" }
	}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"weird-cluster", "weird-cluster",
		"https://weird.example.com",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("weird-cluster")
	if err == nil {
		t.Fatal("expected error for unknown auth shape, got nil")
	}
	if !errors.Is(err, ErrArgoCDProviderUnsupportedAuth) {
		t.Errorf("errors.Is(err, ErrArgoCDProviderUnsupportedAuth) = false, error: %v", err)
	}

	var pe *ArgoCDProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error is not *ArgoCDProviderError: %T", err)
	}
	if pe.Code != ArgoCDProviderCodeUnsupportedAuth {
		t.Errorf("Code = %q, want %q", pe.Code, ArgoCDProviderCodeUnsupportedAuth)
	}
}

// TestArgoCD_NotFound covers the no-matching-secret path. The wrapped error
// should satisfy apierrors.IsNotFound for callers that switch on it.
func TestArgoCD_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset() // no secrets at all
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("does-not-exist")
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected wrapped k8s NotFound, got: %v", err)
	}
}

// TestArgoCD_MalformedConfigJSON covers the case where the config blob isn't
// valid JSON. Should NOT return an ArgoCDProviderError — this is a parse error
// surfaced as-is so callers see it as a malformed-secret problem, not an
// auth-shape decision.
func TestArgoCD_MalformedConfigJSON(t *testing.T) {
	client := fake.NewSimpleClientset(argoCDSecret(
		"broken", "broken",
		"https://broken.example.com",
		`{ this is not valid json `,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("broken")
	if err == nil {
		t.Fatal("expected JSON parse error, got nil")
	}
	if errors.Is(err, ErrArgoCDProviderIAMRequired) ||
		errors.Is(err, ErrArgoCDProviderExecUnsupported) ||
		errors.Is(err, ErrArgoCDProviderUnsupportedAuth) {
		t.Errorf("malformed JSON should not produce a typed routing error, got: %v", err)
	}
}

// TestArgoCD_ListClusters_OnlyClusterTypeMatched verifies that ListClusters
// only returns Secrets carrying the argocd.argoproj.io/secret-type=cluster
// label. The fake clientset honours LabelSelector so secrets without the
// label are excluded by the same path the production code takes.
func TestArgoCD_ListClusters_OnlyClusterTypeMatched(t *testing.T) {
	clusterSecret := argoCDSecret(
		"prod-eu", "prod-eu",
		"https://api.example.com",
		`{"bearerToken":"x","tlsClientConfig":{"insecure":true}}`,
	)
	// A non-cluster secret in the same namespace — must not appear in results.
	otherSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-initial-admin-secret",
			Namespace: "argocd",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "argocd",
			},
		},
		Data: map[string][]byte{"password": []byte("hunter2")},
	}

	client := fake.NewSimpleClientset(clusterSecret, otherSecret)
	provider := newArgoCDProviderWithClient(client, "argocd")

	clusters, err := provider.ListClusters()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected exactly 1 cluster, got %d (raw: %+v)", len(clusters), clusters)
	}
	if clusters[0].Name != "prod-eu" {
		t.Errorf("Name = %q, want %q", clusters[0].Name, "prod-eu")
	}
}

// TestArgoCD_HealthCheck_Success exercises the happy path.
func TestArgoCD_HealthCheck_Success(t *testing.T) {
	client := fake.NewSimpleClientset()
	provider := newArgoCDProviderWithClient(client, "argocd")

	if err := provider.HealthCheck(context.Background()); err != nil {
		t.Errorf("HealthCheck() = %v, want nil", err)
	}
}

// TestArgoCD_HealthCheck_FailsOnPermissionError simulates an RBAC denial from
// the API by installing a reactor that returns Forbidden. The wrapped error
// must remain detectable via apierrors.IsForbidden so the API layer can
// surface it as an actionable RBAC problem.
func TestArgoCD_HealthCheck_FailsOnPermissionError(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("list", "secrets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			corev1.Resource("secrets"), "", errors.New("forbidden"),
		)
	})

	provider := newArgoCDProviderWithClient(client, "argocd")
	err := provider.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected HealthCheck to fail with permission error, got nil")
	}
	if !apierrors.IsForbidden(err) {
		t.Errorf("expected wrapped Forbidden error, got: %v", err)
	}
}

// TestResolveArgoCDNamespaceTyped covers the V125-1-11.4 canonical behaviour:
// the typed ClusterTestProviderConfig.ArgoCDNamespace field is the single
// authoritative source. SHARKO_ARGOCD_NAMESPACE env var remains a deprecated
// compat alias (slog.Warn). The hardcoded "argocd" default applies last.
func TestResolveArgoCDNamespaceTyped(t *testing.T) {
	tests := []struct {
		name        string
		cfg         ClusterTestProviderConfig
		envValue    string // empty string → unset
		setEnv      bool   // true → call t.Setenv even if envValue is ""
		wantNS      string
		description string
	}{
		{
			name:        "default_when_cfg_empty_and_no_env",
			cfg:         ClusterTestProviderConfig{Type: "argocd"},
			wantNS:      "argocd",
			description: "no inputs → hardcoded argocd default",
		},
		{
			name:        "canonical_field_used",
			cfg:         ClusterTestProviderConfig{Type: "argocd", ArgoCDNamespace: "custom"},
			wantNS:      "custom",
			description: "ClusterTestProviderConfig.ArgoCDNamespace is the canonical source",
		},
		{
			name:        "canonical_field_wins_over_env",
			cfg:         ClusterTestProviderConfig{Type: "argocd", ArgoCDNamespace: "from-config"},
			envValue:    "from-env",
			setEnv:      true,
			wantNS:      "from-config",
			description: "typed config field takes precedence over the deprecated env var",
		},
		{
			name:        "env_var_used_when_field_empty",
			cfg:         ClusterTestProviderConfig{Type: "argocd"},
			envValue:    "legacy-ns",
			setEnv:      true,
			wantNS:      "legacy-ns",
			description: "SHARKO_ARGOCD_NAMESPACE deprecated compat alias still works",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv("SHARKO_ARGOCD_NAMESPACE", tt.envValue)
			} else {
				// Defensive: if a parent process has SHARKO_ARGOCD_NAMESPACE set,
				// neutralise it for tests that expect the hardcoded default.
				t.Setenv("SHARKO_ARGOCD_NAMESPACE", "")
			}

			got := resolveArgoCDNamespaceTyped(tt.cfg)
			if got != tt.wantNS {
				t.Errorf("resolveArgoCDNamespaceTyped(%+v) = %q, want %q (%s)",
					tt.cfg, got, tt.wantNS, tt.description)
			}
		})
	}
}

// TestResolveArgoCDNamespaceTyped_DeprecationWarnEmitted confirms the
// SHARKO_ARGOCD_NAMESPACE compat alias emits a slog.Warn so operators see they
// are on the legacy path. The warn is the deprecation signal; removed in v1.26
// per V125-1-11 planning doc OQ #4.
func TestResolveArgoCDNamespaceTyped_DeprecationWarnEmitted(t *testing.T) {
	t.Setenv("SHARKO_ARGOCD_NAMESPACE", "legacy-ns")

	// Capture slog output through a buffer + JSON handler, restoring the
	// default at test end so other tests aren't affected.
	var buf bytes.Buffer
	prevDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevDefault) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	got := resolveArgoCDNamespaceTyped(ClusterTestProviderConfig{Type: "argocd"})
	if got != "legacy-ns" {
		t.Fatalf("resolveArgoCDNamespaceTyped(empty field, env=legacy-ns) = %q, want %q", got, "legacy-ns")
	}
	out := buf.String()
	if !strings.Contains(out, "SHARKO_ARGOCD_NAMESPACE") || !strings.Contains(out, "deprecated") {
		t.Errorf("expected deprecation slog.Warn mentioning SHARKO_ARGOCD_NAMESPACE + deprecated, got: %q", out)
	}
	if !strings.Contains(out, "legacy-ns") {
		t.Errorf("expected warn payload to include the env value %q, got: %q", "legacy-ns", out)
	}
}

// TestResolveArgoCDNamespaceTyped_NoWarnWhenCanonicalFieldUsed confirms the
// happy path is silent — operators on the canonical typed config do NOT see
// the deprecation warn.
func TestResolveArgoCDNamespaceTyped_NoWarnWhenCanonicalFieldUsed(t *testing.T) {
	// Set env var so we can prove the canonical field's precedence suppresses
	// the warn (env wouldn't be consulted at all).
	t.Setenv("SHARKO_ARGOCD_NAMESPACE", "should-be-ignored")

	var buf bytes.Buffer
	prevDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevDefault) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	got := resolveArgoCDNamespaceTyped(ClusterTestProviderConfig{Type: "argocd", ArgoCDNamespace: "from-config"})
	if got != "from-config" {
		t.Fatalf("resolveArgoCDNamespaceTyped(canonical field) = %q, want %q", got, "from-config")
	}
	if strings.Contains(buf.String(), "deprecated") {
		t.Errorf("did not expect deprecation warn when canonical field is set, got: %q", buf.String())
	}
}

// TestResolveArgoCDNamespaceTyped_CrossContaminationStaysClosed is the
// V125-1-11.6 successor to the V125-1-10.8 compat-shim regression check.
// With providers.Config retired, the cross-contamination from k8s-secrets
// addon-secret config into ArgoCD namespace resolution is now structurally
// impossible — there's no shared field to leak through. This test pins the
// canonical resolution behaviour: empty ClusterTestProviderConfig.ArgoCDNamespace
// + unset SHARKO_ARGOCD_NAMESPACE → defaults to "argocd", regardless of any
// other config in the program.
func TestResolveArgoCDNamespaceTyped_CrossContaminationStaysClosed(t *testing.T) {
	t.Setenv("SHARKO_ARGOCD_NAMESPACE", "")
	cfg := ClusterTestProviderConfig{Type: "argocd"} // no ArgoCDNamespace
	if got := resolveArgoCDNamespaceTyped(cfg); got != "argocd" {
		t.Errorf("unset typed namespace + unset env should resolve to default %q, got %q", "argocd", got)
	}
}

// ----- isKnownAWSExecCommand / parseExecProviderAWSArgs unit tests --------
// (V2-cleanup-88.2 — pure-function coverage of the parse-don't-run helpers,
// independent of the fake-client GetCredentials integration tests above.)

func TestIsKnownAWSExecCommand(t *testing.T) {
	tests := []struct {
		name string
		cfg  *argoCDExecProvider
		want bool
	}{
		{"aws_iam_authenticator_no_args", &argoCDExecProvider{Command: "aws-iam-authenticator"}, true},
		{"aws_iam_authenticator_with_args", &argoCDExecProvider{Command: "aws-iam-authenticator", Args: []string{"token", "-i", "x"}}, true},
		{"argocd_k8s_auth_aws_subcommand", &argoCDExecProvider{Command: "argocd-k8s-auth", Args: []string{"aws", "--cluster-name", "x"}}, true},
		{"argocd_k8s_auth_gcp_subcommand", &argoCDExecProvider{Command: "argocd-k8s-auth", Args: []string{"gcp"}}, false},
		{"argocd_k8s_auth_no_args", &argoCDExecProvider{Command: "argocd-k8s-auth"}, false},
		{"argocd_k8s_auth_empty_first_arg", &argoCDExecProvider{Command: "argocd-k8s-auth", Args: []string{""}}, false},
		{"gcloud_unknown", &argoCDExecProvider{Command: "gke-gcloud-auth-plugin"}, false},
		{"az_unknown", &argoCDExecProvider{Command: "az"}, false},
		{"empty_command", &argoCDExecProvider{Command: ""}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isKnownAWSExecCommand(tt.cfg); got != tt.want {
				t.Errorf("isKnownAWSExecCommand(%+v) = %v, want %v", tt.cfg, got, tt.want)
			}
		})
	}
}

func TestParseExecProviderAWSArgs(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		wantClusterName string
		wantRoleARN     string
	}{
		{"empty_args", nil, "", ""},
		{
			"space_separated_both",
			[]string{"aws", "--cluster-name", "my-cluster", "--role-arn", "arn:aws:iam::123456789012:role/example"},
			"my-cluster", "arn:aws:iam::123456789012:role/example",
		},
		{
			"equals_form_both",
			[]string{"aws", "--cluster-name=my-cluster", "--role-arn=arn:aws:iam::123456789012:role/example"},
			"my-cluster", "arn:aws:iam::123456789012:role/example",
		},
		{
			"mixed_forms",
			[]string{"aws", "--cluster-name=my-cluster", "--role-arn", "arn:aws:iam::123456789012:role/example"},
			"my-cluster", "arn:aws:iam::123456789012:role/example",
		},
		{
			"cluster_name_only_role_optional",
			[]string{"aws", "--cluster-name", "my-cluster"},
			"my-cluster", "",
		},
		{
			"role_arn_only",
			[]string{"aws", "--role-arn", "arn:aws:iam::123456789012:role/example"},
			"", "arn:aws:iam::123456789012:role/example",
		},
		{
			"trailing_flag_no_value_ignored",
			[]string{"aws", "--cluster-name"},
			"", "",
		},
		{
			"unknown_flags_interspersed_ignored",
			[]string{"aws", "--profile", "default", "--cluster-name", "my-cluster", "--role-arn", "arn:aws:iam::123456789012:role/example", "--extra"},
			"my-cluster", "arn:aws:iam::123456789012:role/example",
		},
		{
			"order_reversed",
			[]string{"aws", "--role-arn", "arn:aws:iam::123456789012:role/example", "--cluster-name", "my-cluster"},
			"my-cluster", "arn:aws:iam::123456789012:role/example",
		},
		{
			"equals_form_empty_value",
			[]string{"aws", "--cluster-name="},
			"", "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCluster, gotRole := parseExecProviderAWSArgs(tt.args)
			if gotCluster != tt.wantClusterName {
				t.Errorf("parseExecProviderAWSArgs(%v) clusterName = %q, want %q", tt.args, gotCluster, tt.wantClusterName)
			}
			if gotRole != tt.wantRoleARN {
				t.Errorf("parseExecProviderAWSArgs(%v) roleARN = %q, want %q", tt.args, gotRole, tt.wantRoleARN)
			}
		})
	}
}
