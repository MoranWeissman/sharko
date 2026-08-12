package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// secretsManagerClient is the slice of the AWS Secrets Manager API this
// provider uses. *secretsmanager.Client satisfies it; tests satisfy it
// with a fake so the addon-secret boundary's allowed path can be proven
// without real AWS credentials. Method shapes match the SDK's generated
// client (secretsmanager v1.41.5); ListSecrets comes in via the SDK's own
// paginator interface so NewListSecretsPaginator accepts the field as-is.
type secretsManagerClient interface {
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
	secretsmanager.ListSecretsAPIClient
}

// AWSSecretsManagerProvider reads kubeconfigs from AWS Secrets Manager.
// Secret path: {prefix}{cluster-name}. Supports IRSA for authentication.
type AWSSecretsManagerProvider struct {
	client  secretsManagerClient
	prefix  string
	roleARN string // default IAM role to assume for EKS token generation

	// eksTokenFn is the per-instance test seam over getEKSToken (STS
	// presigned-URL token minting). Defaulted in the constructors;
	// overridden in unit tests so the structured-EKS payload path can be
	// exercised without real AWS credentials.
	eksTokenFn func(ctx context.Context, clusterName, region, roleARN string) (string, error)
}

// newAWSSecretsManagerProvider is the shared builder behind both public
// constructors. Uses the default AWS credential chain (IRSA when in-cluster,
// env vars or profile for local dev). No default prefix — secret name equals
// cluster name unless a prefix is configured.
func newAWSSecretsManagerProvider(region, prefix, roleARN string) (*AWSSecretsManagerProvider, error) {
	opts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	return &AWSSecretsManagerProvider{
		client:     secretsmanager.NewFromConfig(awsCfg),
		prefix:     prefix,
		roleARN:    roleARN,
		eksTokenFn: getEKSToken,
	}, nil
}

// NewAWSSecretsManagerProviderFromAddonConfig creates a provider backed by AWS
// Secrets Manager from the canonical AddonSecretProviderConfig.
//
// Only AddonSecretProviderConfig fields Region, Prefix, and RoleARN are read —
// Type is consumed by the upstream dispatcher (NewAddonSecretProvider) and
// Namespace is ignored (AWS Secrets Manager has no namespace concept).
func NewAWSSecretsManagerProviderFromAddonConfig(cfg AddonSecretProviderConfig) (*AWSSecretsManagerProvider, error) {
	return newAWSSecretsManagerProvider(cfg.Region, cfg.Prefix, cfg.RoleARN)
}

// NewAWSSecretsManagerProviderFromClusterTestConfig creates a provider backed
// by AWS Secrets Manager from the canonical ClusterTestProviderConfig — the
// cluster-credentials arm restored by V2-cleanup-53.1 so registrations with
// creds_source=secret-kubeconfig / eks-token reach AWS SM. Reads Region,
// Prefix, and RoleARN; Type is consumed by the upstream dispatcher
// (NewClusterTestProvider) and ArgoCDNamespace/Namespace are other backends'
// fields.
func NewAWSSecretsManagerProviderFromClusterTestConfig(cfg ClusterTestProviderConfig) (*AWSSecretsManagerProvider, error) {
	return newAWSSecretsManagerProvider(cfg.Region, cfg.Prefix, cfg.RoleARN)
}

// GetSecretValue retrieves a raw secret value from AWS Secrets Manager.
// path is the full secret name in AWS Secrets Manager, and it must sit
// under the configured prefix — see checkAddonSecretPathAllowed. The
// boundary check runs BEFORE the AWS call, so a refused path never
// reaches AWS at all.
func (p *AWSSecretsManagerProvider) GetSecretValue(ctx context.Context, path string) ([]byte, error) {
	slog.Debug("[provider] GetSecretValue called", "path", path)
	if err := p.checkAddonSecretPathAllowed(path); err != nil {
		slog.Warn("[provider] GetSecretValue refused: path outside the configured boundary", "path", path, "prefix", p.prefix)
		return nil, err
	}
	output, err := p.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(path),
	})
	if err != nil {
		return nil, fmt.Errorf("getting secret %q from AWS Secrets Manager: %w", path, err)
	}
	if output.SecretString != nil {
		value := []byte(*output.SecretString)
		slog.Debug("[provider] GetSecretValue success", "path", path)
		return value, nil
	}
	if output.SecretBinary != nil {
		slog.Debug("[provider] GetSecretValue success", "path", path)
		return output.SecretBinary, nil
	}
	return nil, fmt.Errorf("secret %q has no value", path)
}

// checkAddonSecretPathAllowed is the addon-secret read boundary (task
// #152 story B). The configured prefix is the whole allowlist: a path is
// allowed only when it starts with the prefix, and an empty prefix
// refuses everything instead of silently allowing the whole AWS account.
//
// The prefix is matched literally, exactly as it is used everywhere else
// in this provider ({prefix}{name} concatenation, ListSecrets name
// filter). Operators should end it with a separator such as "/" so a
// prefix like "prod" cannot also match a sibling name like
// "production-secrets/...".
//
// This boundary is only for addon-secret VALUE reads (GetSecretValue).
// The cluster-credential path (GetCredentials / fetchSecret) keeps its
// documented exact-name and secret_path lookups — that surface hands the
// kubeconfig to Sharko itself, it never delivers a value into a managed
// cluster.
func (p *AWSSecretsManagerProvider) checkAddonSecretPathAllowed(path string) error {
	if p.prefix == "" {
		return awsNoPrefixRefusal(path)
	}
	if !strings.HasPrefix(path, p.prefix) {
		return awsOutsidePrefixRefusal(path, p.prefix)
	}
	return nil
}

// structuredEKSSecret is the JSON schema used when secrets contain EKS cluster
// metadata rather than a raw kubeconfig YAML.
type structuredEKSSecret struct {
	ClusterName string `json:"clusterName"`
	Host        string `json:"host"`
	CAData      string `json:"caData"`
	AccountId   string `json:"accountId"`
	Region      string `json:"region"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
	RoleARN     string `json:"roleArn"` // optional — IAM role to assume for cluster access
}

// fetchSecret retrieves and parses credentials from the secret at the given
// exact name. clusterRoleARN is the optional per-cluster IAM role recorded on
// the cluster's managed-clusters.yaml entry (V2-cleanup-62.2); "" means "no
// per-cluster role" and preserves the pre-field behavior exactly. It only
// participates in the structured-EKS (token-mint) path — a raw-kubeconfig
// payload never mints, so the value is ignored there.
func (p *AWSSecretsManagerProvider) fetchSecret(secretName, clusterRoleARN string) (*Kubeconfig, error) {
	output, err := p.client.GetSecretValue(context.Background(), &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	})
	if err != nil {
		wrapped := fmt.Errorf("getting secret %q from AWS Secrets Manager: %w", secretName, err)
		// THIS is where "the secret is not there" is actually KNOWN — the SDK
		// says so with its own type. Nothing downstream may infer it from
		// words, so it is recorded as a marker here and read with
		// credsafe.IsNotFound. An AccessDenied, a throttle or a network
		// failure deliberately does NOT get the marker, because a caller acts
		// on it (the cluster-test handler offers secret-name suggestions) and
		// "we were not allowed to look" is not "it is missing".
		var missing *types.ResourceNotFoundException
		if errors.As(err, &missing) {
			return nil, credsafe.MarkNotFound(wrapped)
		}
		return nil, wrapped
	}

	if output.SecretString == nil {
		return nil, fmt.Errorf("secret %q has no string value", secretName)
	}

	raw := []byte(*output.SecretString)

	// Payload sniff: structured EKS-JSON → STS token path; anything else →
	// raw kubeconfig YAML. This dispatch is what makes creds_source=eks-token
	// flow through the aws-sm arm with no separate wiring (V2-cleanup-53.1).
	//
	// No "size" field on either log line below (task #152 story D): raw here
	// is a whole kubeconfig — cluster credentials — and these lines run at
	// Info, not Debug, so they are on by default in production. secretName
	// and format are Git/config metadata and stay.
	if structured, ok := sniffStructuredEKSSecret(raw); ok {
		slog.Info("[provider] secret fetched", "secretName", secretName, "format", "structured")
		return p.buildFromStructured(structured, clusterRoleARN)
	}

	// Fallback: treat the secret value as raw kubeconfig YAML.
	slog.Info("[provider] secret fetched", "secretName", secretName, "format", "raw")
	return p.buildFromRawKubeconfig(raw, secretName)
}

// sniffStructuredEKSSecret reports whether a secret payload is the structured
// EKS-JSON descriptor (as opposed to raw kubeconfig YAML). The signal is
// "parses as JSON AND has a non-empty host" — kubeconfig YAML fails the JSON
// parse. Extracted so the dispatch decision is unit-testable without an SM
// client.
func sniffStructuredEKSSecret(raw []byte) (structuredEKSSecret, bool) {
	var structured structuredEKSSecret
	if err := json.Unmarshal(raw, &structured); err == nil && structured.Host != "" {
		return structured, true
	}
	return structuredEKSSecret{}, false
}

// GetCredentials fetches credentials for the named cluster using a multi-step lookup:
//
//  1. Try with the configured prefix (e.g. "clusters/prod-eu").
//  2. Try the exact name as-is (supports secretPath passthrough from the orchestrator).
//  3. Try common name patterns (k8s-, eks-, cluster- prefixes) using GetSecretValue only.
//  4. Fall back to ListSecrets-based search if permitted; if AccessDenied, skip gracefully.
func (p *AWSSecretsManagerProvider) GetCredentials(clusterName string) (*Kubeconfig, error) {
	return p.GetCredentialsWithRoleARN(clusterName, "")
}

// GetCredentialsWithRoleARN is GetCredentials with an optional per-cluster
// IAM role for EKS token minting (V2-cleanup-62.2 — the
// RoleARNCredentialsProvider capability). clusterRoleARN is the roleArn
// recorded on the cluster's managed-clusters.yaml entry at registration;
// "" is byte-identical to GetCredentials. See buildFromStructured for the
// precedence decision (SM-secret roleArn > clusterRoleARN > provider default).
// credsafe.Mark is applied at the boundary — one place every return passes
// through, so a new failure branch inside cannot forget it. After Mark, the
// returned error's Error() is the fixed safe sentence; what a caller can still
// learn is carried by TYPE, and for this method that is credsafe.IsNotFound
// (see below for when it is set and when it deliberately is not).
func (p *AWSSecretsManagerProvider) GetCredentialsWithRoleARN(clusterName, clusterRoleARN string) (*Kubeconfig, error) {
	kc, err := p.getCredentialsWithRoleARN(clusterName, clusterRoleARN)
	return kc, credsafe.Mark(err)
}

func (p *AWSSecretsManagerProvider) getCredentialsWithRoleARN(clusterName, clusterRoleARN string) (*Kubeconfig, error) {
	slog.Info("[provider] GetCredentials called", "cluster", clusterName)

	var tried []string
	// allMissing tracks whether EVERY attempt that ran failed because AWS said
	// the secret does not exist. It starts true and only one non-missing failure
	// (AccessDenied, a throttle, a network error, an unparseable payload) turns
	// it off for good — because in that case Sharko does not KNOW the secret is
	// absent, it only knows it could not read it. The caller offers secret-name
	// suggestions off this answer, and offering "did you mean one of these?"
	// when the real problem is a missing IAM permission sends the operator to
	// fix the wrong thing.
	allMissing := true
	note := func(err error) {
		if !credsafe.IsNotFound(err) {
			allMissing = false
		}
	}

	// Step 1: Try with prefix (skipped when prefix is empty or name already contains prefix).
	if p.prefix != "" {
		withPrefix := p.prefix + clusterName
		tried = append(tried, withPrefix)
		slog.Debug("[provider] trying with prefix", "secretName", withPrefix)
		kc, err := p.fetchSecret(withPrefix, clusterRoleARN)
		if err == nil {
			return kc, nil
		}
		note(err)
	}

	// Step 2: Try exact name (handles explicit secretPath values that don't need a prefix).
	if !sliceContains(tried, clusterName) {
		tried = append(tried, clusterName)
	}
	slog.Debug("[provider] trying exact name", "secretName", clusterName)
	kc, err := p.fetchSecret(clusterName, clusterRoleARN)
	if err == nil {
		return kc, nil
	}
	note(err)

	// Every lookup failed. Return an error — the caller (handleTestCluster) will
	// call SearchSecrets separately to find suggestions and include them in the
	// response, but ONLY when this error carries the not-found marker.
	//
	// The sentence below still says "not found" because it is Sharko's own
	// wording for a person, unchanged. Nothing reads it: the caller asks
	// credsafe.IsNotFound, which is a type check.
	slog.Error("[provider] GetCredentials failed", "cluster", clusterName, "step", "all-lookups",
		"tried", strings.Join(tried, ", "), "allMissing", allMissing)
	failure := fmt.Errorf("secret for cluster %q not found in AWS Secrets Manager. Tried: %s. "+
		"Set secret_path on the cluster to specify the exact name",
		clusterName, strings.Join(tried, ", "))
	if allMissing {
		return nil, credsafe.MarkNotFound(failure)
	}
	return nil, failure
}

// StoredConnectionFacts reports what this backend has STORED for the named
// cluster, and never mints anything.
//
// It runs the same two-step lookup GetCredentials does (prefix first, then the
// exact name) so a cluster found by one is found by the other, and then it
// STOPS at the payload. On the structured-EKS branch that means it reports the
// server and the CA bundle and sets CredentialMintedPerFetch — the stored
// payload holds no credential, only what would be needed to create one, and
// this method does NOT go on to buildFromStructured, which is where the STS
// token gets created. On the raw-kubeconfig branch the payload holds a fixed
// credential already, so it is reported as it stands and nothing is created
// there either.
//
// This is the method the read-only connection comparison uses. GetCredentials
// stays exactly as it was for every caller that needs credentials that work.
func (p *AWSSecretsManagerProvider) StoredConnectionFacts(lookupKey string) (*StoredConnectionFacts, error) {
	raw, err := p.rawPayloadForCluster(lookupKey)
	if err != nil {
		return nil, credsafe.Mark(err)
	}

	if structured, ok := sniffStructuredEKSSecret(raw); ok {
		// The stored payload is EKS metadata. There is no credential in it —
		// only what is needed to create one — and this method creates nothing.
		caData, decodeErr := base64.StdEncoding.DecodeString(structured.CAData)
		if decodeErr != nil {
			// A decode failure's text is produced from the stored payload, so
			// it is marked like every other failure on this path.
			return nil, credsafe.Mark(fmt.Errorf("decoding caData for cluster %q: %w", structured.ClusterName, decodeErr))
		}
		slog.Info("[provider] stored connection details read without minting a sign-in token",
			"secretName", lookupKey, "format", "structured")
		return &StoredConnectionFacts{
			Server:                   structured.Host,
			CAData:                   caData,
			CredentialMintedPerFetch: true,
		}, nil
	}

	// A raw kubeconfig holds a fixed credential. Parsing it mints nothing.
	kc, err := p.buildFromRawKubeconfig(raw, lookupKey)
	if err != nil {
		// A kubeconfig parse error is produced FROM the credential material,
		// so client-go's message can quote part of it back.
		return nil, credsafe.Mark(err)
	}
	slog.Info("[provider] stored connection details read without minting a sign-in token",
		"secretName", lookupKey, "format", "raw")
	return &StoredConnectionFacts{
		Server:   kc.Server,
		CAData:   kc.CAData,
		Token:    kc.Token,
		CertData: kc.CertData,
		KeyData:  kc.KeyData,
	}, nil
}

// rawPayloadForCluster fetches the stored secret bytes for a cluster using the
// same prefix-then-exact-name lookup GetCredentials uses, and does nothing else
// with them. No sniff, no parse, no mint.
func (p *AWSSecretsManagerProvider) rawPayloadForCluster(clusterName string) ([]byte, error) {
	var tried []string
	if p.prefix != "" {
		withPrefix := p.prefix + clusterName
		tried = append(tried, withPrefix)
		if raw, err := p.rawSecretValue(withPrefix); err == nil {
			return raw, nil
		}
	}
	if !sliceContains(tried, clusterName) {
		tried = append(tried, clusterName)
	}
	if raw, err := p.rawSecretValue(clusterName); err == nil {
		return raw, nil
	}
	return nil, fmt.Errorf("secret for cluster %q not found in AWS Secrets Manager. Tried: %s. "+
		"Set secret_path on the cluster to specify the exact name",
		clusterName, strings.Join(tried, ", "))
}

// rawSecretValue reads one secret's string payload by exact name.
func (p *AWSSecretsManagerProvider) rawSecretValue(secretName string) ([]byte, error) {
	output, err := p.client.GetSecretValue(context.Background(), &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	})
	if err != nil {
		return nil, fmt.Errorf("getting secret %q from AWS Secrets Manager: %w", secretName, err)
	}
	if output.SecretString == nil {
		return nil, fmt.Errorf("secret %q has no string value", secretName)
	}
	return []byte(*output.SecretString), nil
}

// sliceContains checks whether a string slice contains a value.
func sliceContains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// SearchSecrets returns secret names that contain query as a substring.
// Uses ListSecrets with a name filter. If ListSecrets fails (e.g. AccessDenied),
// returns an empty list and nil error to degrade gracefully.
func (p *AWSSecretsManagerProvider) SearchSecrets(query string) ([]string, error) {
	results, err := p.searchSimilar(query)
	if err != nil {
		// The AWS error's own value is not logged. This is the same rule the
		// mint-failure branch follows, and the log-source guard caught this line
		// when the guard was extended to this file — a real leak the brief's
		// file:line list did not name. The query (a cluster name the caller
		// already sent) and the step are what a person needs; the outcome is
		// unchanged, an empty suggestion list.
		slog.Warn("[provider] SearchSecrets failed (likely AccessDenied, returning empty)", "query", query, "prefix", p.prefix, "step", "list-secrets")
		return nil, nil
	}
	return results, nil
}

// searchSimilar returns secret names that contain query as a substring.
// The ListSecrets filter with key=name does substring matching on the secret name.
func (p *AWSSecretsManagerProvider) searchSimilar(query string) ([]string, error) {
	slog.Info("[provider] searching for similar secrets", "query", query)
	paginator := secretsmanager.NewListSecretsPaginator(p.client, &secretsmanager.ListSecretsInput{
		Filters: []types.Filter{
			{
				Key:    types.FilterNameStringTypeName,
				Values: []string{query},
			},
		},
	})

	var matches []string
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.Background())
		if err != nil {
			return nil, err
		}
		for _, secret := range page.SecretList {
			matches = append(matches, aws.ToString(secret.Name))
		}
	}
	slog.Info("[provider] similar secrets search complete", "query", query, "found", len(matches))
	return matches, nil
}

// buildFromStructured constructs a Kubeconfig from the EKS JSON metadata format.
// It base64-decodes the CA cert, obtains a short-lived STS bearer token, and
// builds a kubeconfig YAML for use by remoteclient.NewClientFromKubeconfig.
// clusterRoleARN is the optional per-cluster role from the cluster's
// managed-clusters.yaml entry ("" = none stored).
func (p *AWSSecretsManagerProvider) buildFromStructured(s structuredEKSSecret, clusterRoleARN string) (*Kubeconfig, error) {
	hostPreview := s.Host
	if len(hostPreview) > 30 {
		hostPreview = hostPreview[:30] + "..."
	}
	slog.Debug("[provider] parsed structured JSON",
		"cluster", s.ClusterName,
		"host", hostPreview,
		"region", s.Region,
		"hasCAData", s.CAData != "",
	)

	caData, err := base64.StdEncoding.DecodeString(s.CAData)
	if err != nil {
		return nil, fmt.Errorf("decoding caData for cluster %q: %w", s.ClusterName, err)
	}

	// Determine cluster name: prefer the clusterName field; fall back to empty
	// string which will cause getEKSToken to omit the header (should not happen).
	name := s.ClusterName
	if name == "" {
		name = s.Environment
	}

	// Role-ARN precedence (V2-cleanup-62.2 — the token-mint decision point):
	//
	//   1. The structured SM secret's own roleArn — most specific: it is
	//      stored WITH the credential material for exactly this secret.
	//   2. The per-cluster roleArn recorded on the cluster's
	//      managed-clusters.yaml entry at registration (clusterRoleARN) —
	//      this is how a discovery-registered cross-account cluster mints
	//      tokens with the same identity that discovered it.
	//   3. The connection-level provider default (ProviderConfig.RoleARN →
	//      p.roleARN) — the coarsest fallback.
	//
	// Pinned by TestBuildFromStructured_RoleARNPrecedence in aws_sm_test.go.
	roleARN := s.RoleARN
	if roleARN == "" {
		roleARN = clusterRoleARN
	}
	if roleARN == "" {
		roleARN = p.roleARN
	}

	tokenFn := p.eksTokenFn
	if tokenFn == nil {
		tokenFn = getEKSToken // nil-safe for struct-literal construction in tests
	}
	token, err := tokenFn(context.Background(), name, s.Region, roleARN)
	if err != nil {
		// THE ERROR VALUE IS NOT LOGGED — same rule as aws_auth.go's two
		// failure branches, and this is the line that receives what they
		// return. An AWS SDK error can carry credential material in its own
		// text: a wrapped presigned URL, a token fragment, a credential a
		// provider chain put into its message. So the log line carries the
		// cluster, the region and WHICH step failed, and nothing else.
		//
		// The returned error still wraps the cause with %w — that is correct
		// and unchanged, so a developer reading a stack still gets the cause.
		// GetCredentialsWithRoleARN marks every error this path returns, so a
		// public boundary can recognise a credentials-backend failure by TYPE
		// (errors.Is against a sentinel, never by reading its words) and say a
		// fixed safe sentence instead.
		slog.Error("[provider] GetCredentials failed", "cluster", name, "region", s.Region, "step", "sts")
		return nil, fmt.Errorf("generating EKS token for cluster %q: %w", name, err)
	}

	// "a token was minted for this cluster" is worth a line. A PREFIX of it is
	// not, and the field that used to carry one is gone.
	//
	// The old comment here argued the first 20 characters were provably the
	// constant "k8s-aws-v1.aHR0cHM6L" and therefore non-secret. The argument
	// was careful and it did not matter. If the value really is a constant then
	// logging it tells an operator nothing they could not read here in the
	// source; and if the assumption ever quietly stopped holding — a different
	// URL scheme, a different prefix length, a non-EKS token flowing through
	// this path — it would start leaking with a comment vouching for it. A
	// prefix of a credential is on the forbidden list for the same reason a
	// length and a hash are: each one narrows a guess at the thing itself.
	slog.Info("[provider] STS token generated", "cluster", name, "region", s.Region)

	// Build kubeconfig YAML so remoteclient.NewClientFromKubeconfig can use Raw.
	// certificate-authority-data expects base64 — use s.CAData (original base64 string).
	kubeconfigYAML := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    certificate-authority-data: %s
  name: %s
contexts:
- context:
    cluster: %s
    user: %s
  name: %s
current-context: %s
users:
- name: %s
  user:
    token: %s
`, s.Host, s.CAData, name, name, name, name, name, name, token)

	slog.Info("[provider] kubeconfig built", "server", s.Host, "hasCA", len(caData) > 0, "hasToken", token != "")
	return &Kubeconfig{
		Raw:    []byte(kubeconfigYAML),
		Server: s.Host,
		CAData: caData,
		Token:  token,
	}, nil
}

// buildFromRawKubeconfig parses a raw kubeconfig YAML secret and extracts
// connection info using the client-go clientcmd library.
func (p *AWSSecretsManagerProvider) buildFromRawKubeconfig(raw []byte, secretName string) (*Kubeconfig, error) {
	kc := &Kubeconfig{Raw: raw}

	config, err := clientcmd.RESTConfigFromKubeConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig from secret %q: %w", secretName, err)
	}

	kc.Server = config.Host
	kc.CAData = config.TLSClientConfig.CAData
	kc.Token = config.BearerToken
	// Carry the client cert pair for cert-based kubeconfigs (kind / kubeadm /
	// on-prem). Only a complete pair is propagated — a half pair would never
	// take the cert branch in the ArgoCD secret writers anyway (V2-cleanup-56.1).
	if len(config.TLSClientConfig.CertData) > 0 && len(config.TLSClientConfig.KeyData) > 0 {
		kc.CertData = config.TLSClientConfig.CertData
		kc.KeyData = config.TLSClientConfig.KeyData
	}

	return kc, nil
}

// HealthCheck confirms AWS Secrets Manager credentials work by calling ListSecrets
// with MaxResults=1 and the configured prefix filter. No secret values are fetched.
func (p *AWSSecretsManagerProvider) HealthCheck(ctx context.Context) error {
	maxResults := int32(1)
	_, err := p.client.ListSecrets(ctx, &secretsmanager.ListSecretsInput{
		MaxResults: &maxResults,
		Filters: []types.Filter{
			{
				Key:    types.FilterNameStringTypeName,
				Values: []string{p.prefix},
			},
		},
	})
	if err != nil {
		// The health check talks to the credentials backend with Sharko's own
		// AWS identity, so its error is the same class as a fetch failure and is
		// marked the same way: Error() becomes the fixed safe sentence, and the
		// AWS message stays reachable through Unwrap for classification.
		return credsafe.Mark(fmt.Errorf("AWS Secrets Manager health check failed: %w", err))
	}
	return nil
}

func (p *AWSSecretsManagerProvider) ListClusters() ([]ClusterInfo, error) {
	slog.Info("[provider] ListClusters called", "prefix", p.prefix)
	var clusters []ClusterInfo
	paginator := secretsmanager.NewListSecretsPaginator(p.client, &secretsmanager.ListSecretsInput{
		Filters: []types.Filter{
			{
				Key:    types.FilterNameStringTypeName,
				Values: []string{p.prefix},
			},
		},
	})

	ctx := context.Background()
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing secrets with prefix %q: %w", p.prefix, err)
		}
		slog.Debug("[provider] ListClusters page", "count", len(page.SecretList))
		for _, secret := range page.SecretList {
			name := strings.TrimPrefix(aws.ToString(secret.Name), p.prefix)
			info := ClusterInfo{Name: name}

			// Extract region from SM tags first (cheap, no extra API call).
			for _, tag := range secret.Tags {
				if aws.ToString(tag.Key) == "region" {
					info.Region = aws.ToString(tag.Value)
				}
			}

			// Try to fetch the secret and parse structured JSON for richer metadata.
			// This adds one API call per cluster, which is acceptable for discovery.
			val, err := p.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
				SecretId: secret.Name,
			})
			if err == nil && val.SecretString != nil {
				var meta struct {
					Region      string `json:"region"`
					Project     string `json:"project"`
					Environment string `json:"environment"`
				}
				if json.Unmarshal([]byte(*val.SecretString), &meta) == nil {
					if meta.Region != "" {
						info.Region = meta.Region
					}
					if meta.Project != "" || meta.Environment != "" {
						if info.Tags == nil {
							info.Tags = make(map[string]string)
						}
						if meta.Project != "" {
							info.Tags["project"] = meta.Project
						}
						if meta.Environment != "" {
							info.Tags["environment"] = meta.Environment
						}
					}
				}
			}

			clusters = append(clusters, info)
		}
	}
	slog.Info("[provider] ListClusters complete", "total", len(clusters))
	return clusters, nil
}
