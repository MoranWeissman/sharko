package demo

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/api"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/capabilities"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/observations"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/prtracker"
	"github.com/MoranWeissman/sharko/internal/settings"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// SetupDemoServer wires up full mock backends and configures the API server
// for QA testing without any external dependencies. It returns a cleanup
// function that should be called on shutdown.
//
// cfg sizes the estate (S1/S2) — cfg.IsDefault() reproduces today's small,
// hand-written estate exactly; any other size generates a fresh one
// (GenerateEstate) shared across the mock Git provider, the mock ArgoCD
// server, the mock credentials provider, and the PR tracker below, so all
// four present the SAME clusters/addons/PRs.
//
// Users created:
//   - admin / admin    (admin role)
//   - qa    / sharko   (viewer role)
func SetupDemoServer(srv *api.Server, cfg ScaleConfig) (cleanup func(), err error) {
	// AWS identity — demo mode must NEVER run real AWS identity detection.
	// Without this, GET /api/v1/system/capabilities falls through to the
	// server's lazy real AWSDetector (internal/capabilities), which calls
	// sts:GetCallerIdentity against the host's ambient AWS credential
	// chain — a local demo instance ended up displaying the maintainer's
	// own real work identity in the UI. Inject a static, obviously-fake
	// detector first, before anything else, so it's in place no matter
	// what happens later in this function.
	srv.SetAWSDetector(capabilities.NewStaticAWSDetector(capabilities.AWSIdentity{
		Detected:    true,
		Method:      "demo",
		IdentityARN: "arn:aws:iam::000000000000:role/sharko-demo",
	}))

	// 0. Generate the estate once (nil for the default size — every
	// mock-* constructor below falls back to the hand-written fixture in
	// that case) so every mock backend agrees on the same data.
	var estate *GeneratedEstate
	if !cfg.IsDefault() {
		estate, err = GenerateEstate(cfg)
		if err != nil {
			return nil, fmt.Errorf("generating demo estate: %w", err)
		}
		slog.Info("demo: generated estate", "clusters", len(estate.Clusters),
			"addons", len(estate.Addons), "seed", cfg.Seed, "component", "demo")
	}

	// 1. Start the mock ArgoCD HTTP server.
	var mockArgocd *MockArgocdServer
	if estate != nil {
		mockArgocd, err = NewMockArgocdServerFromEstate(estate)
	} else {
		mockArgocd, err = NewMockArgocdServer()
	}
	if err != nil {
		return nil, fmt.Errorf("starting mock argocd server: %w", err)
	}
	slog.Info("demo: mock argocd listening", "url", mockArgocd.URL(), "component", "demo")

	// 2. Build an in-memory config store with a demo connection pointing at the mock.
	store := newInMemoryStore()
	conn := models.Connection{
		Name:        "demo/sharko-addons",
		Description: "Demo connection — mock ArgoCD + in-memory Git",
		Git: models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			Owner:    "demo",
			Repo:     "sharko-addons",
			Token:    "demo-token",
		},
		Argocd: models.ArgocdConfig{
			ServerURL: mockArgocd.URL(),
			Token:     "demo-argocd-token",
			Namespace: "argocd",
			Insecure:  true,
		},
		IsDefault: true,
	}
	if err := store.SaveConnection(conn); err != nil {
		mockArgocd.Close()
		return nil, fmt.Errorf("saving demo connection: %w", err)
	}

	// 3. Replace the connection service's store with the in-memory one.
	// The demo store has the correct connection pointing at our mock ArgoCD server.
	srv.SetDemoConnectionService(store)

	// 4. Set the mock Git provider so write operations work without real GitHub calls.
	var mockGit *MockGitProvider
	if estate != nil {
		mockGit, err = NewMockGitProviderFromEstate(estate)
		if err != nil {
			mockArgocd.Close()
			return nil, fmt.Errorf("building mock git provider: %w", err)
		}
	} else {
		mockGit = NewMockGitProvider()
	}
	srv.SetDemoGitProvider(mockGit)

	// 5. Configure the secrets provider for cluster registration.
	//
	// Demo mode constructs a MockClusterCredentialsProvider directly
	// (no real backend); the two typed configs are stashed with the
	// demo Type so /providers + /config surface a recognizable
	// placeholder. addonSecretCfg.RoleARN stays empty — demo
	// orchestrator paths handle that via nil-guards in handlers.
	//
	// G2 (gitops-proud P4-G): this exact Type ("demo") is what
	// (*Server).addonValuesSecretSourceLabel (internal/api/system_managed_secrets.go)
	// resolves to "the demo secrets store" — the Managed Secrets page's
	// SOURCE column, so `make demo-big` shows a real name on every addon-
	// values row instead of the generic "secrets store" fallback.
	var credProvider *MockClusterCredentialsProvider
	if estate != nil {
		credProvider = NewMockClusterCredentialsProviderFromEstate(estate)
	} else {
		credProvider = &MockClusterCredentialsProvider{}
	}
	addonCfg := &providers.AddonSecretProviderConfig{
		Type:   "demo",
		Region: "demo",
	}
	testCfg := &providers.ClusterTestProviderConfig{
		Type: "demo",
	}
	repoPaths := orchestrator.RepoPathsConfig{
		ClusterValues: "configuration/addons-clusters-values",
		GlobalValues:  "configuration/addons-global-values",
		Catalog:       "configuration/addons-catalog.yaml",
		Charts:        "charts/",
		Bootstrap:     "bootstrap/",
	}
	gitopsCfg := orchestrator.GitOpsConfig{
		PRAutoMerge:  false,
		BranchPrefix: "sharko/",
		CommitPrefix: "sharko:",
		BaseBranch:   "main",
		RepoURL:      "https://github.com/demo/sharko-addons",
	}
	srv.SetWriteAPIDeps(credProvider, addonCfg, testCfg, repoPaths, gitopsCfg)

	// 6. Addon secret definitions. datadog + vault are the ORIGINAL 2 demo
	// definitions — kept exactly as they were so the small hand-written
	// estate (plain `make demo`) is unchanged. A generated (non-default)
	// estate additionally gets definitions for a handful of addons it
	// actually enables (see demoGeneratedAddonSecretDefs), so the Managed
	// Secrets page has real addon-values rows to show at scale (maintainer's
	// ask). Gated behind estate != nil for the same reason step 10 gates
	// its own additions: some of these addon names (e.g. cert-manager) are
	// ALSO enabled on the small hand-written estate's clusters, and adding
	// their definitions unconditionally would grow that estate's managed
	// secrets rows too — a change, not the fix this is meant to be.
	addonSecretDefs := map[string]orchestrator.AddonSecretDefinition{
		"datadog": {
			AddonName:  "datadog",
			SecretName: "datadog-secrets",
			Namespace:  "datadog",
			Keys: map[string]string{
				"api-key": "secrets/datadog/api-key",
				"app-key": "secrets/datadog/app-key",
			},
		},
		"vault": {
			AddonName:  "vault",
			SecretName: "vault-unseal-keys",
			Namespace:  "vault",
			Keys: map[string]string{
				"unseal-key": "secrets/vault/unseal-key",
			},
		},
	}
	if estate != nil {
		for name, def := range demoGeneratedAddonSecretDefs() {
			addonSecretDefs[name] = def
		}
	}
	srv.SetAddonSecretDefs(addonSecretDefs)

	// 7. Default addons.
	srv.SetDefaultAddons(map[string]bool{
		"cert-manager":   true,
		"metrics-server": true,
	})

	// 8. Create demo users: admin/admin and qa/sharko.
	// We use the AddDemoUser method which the api.Server exposes for demo mode.
	if err := srv.AddDemoUser("admin", "admin", "admin"); err != nil {
		slog.Warn("demo: could not create admin user", "error", err, "component", "demo")
	}
	if err := srv.AddDemoUser("qa", "sharko", "viewer"); err != nil {
		slog.Warn("demo: could not create qa user", "error", err, "component", "demo")
	}

	// 8b. Server settings (gitops-proud P4-I, D2) — GET/PUT
	// /api/v1/settings/addon-values-engine-enabled (Settings → Addon Values
	// Engine, and the Secret Sync page's engine strip) both need a real,
	// working settings.Store. Without one, s.settingsStore stays nil: the
	// GET still answers (nil-safe, static default true), but the PUT
	// 503s — the off switch would be unreachable in demo, which is exactly
	// what the demo rule for this feature says not to ship. Backed by its
	// own small fake Kubernetes clientset, same "cmstore.Store just needs
	// SOMETHING implementing kubernetes.Interface" reasoning as prCMStore
	// below — nothing here ever talks to a real cluster, and the setting
	// persists in-memory for the life of this demo process, the same way a
	// real install's ConfigMap persists for the life of the pod. Wired
	// unconditionally (not inside the `if estate != nil` block below) so
	// it's reachable from plain `make demo` too, not just `make demo-big`.
	demoSettingsStore := settings.NewStore(k8sfake.NewSimpleClientset(), "sharko")
	srv.SetSettingsStore(demoSettingsStore)

	// 9. PR tracker — /api/v1/prs reads from srv.prTracker, which is nil
	// unless something wires it up. The real (non-demo) path only does
	// that deep inside the Kubernetes-mode provider setup, which demo mode
	// never reaches, so without this GET /api/v1/prs silently returned an
	// empty list forever. Backed by an in-memory fake Kubernetes clientset
	// (cmstore.Store just needs SOMETHING implementing kubernetes.Interface;
	// nothing here ever talks to a real cluster) and seeded directly via
	// TrackPR — no polling goroutine is started, since a static demo
	// estate never changes and the mock Git provider has no matching
	// branches for the tracker's poll loop to reconcile against anyway.
	prCMStore := cmstore.NewStore(k8sfake.NewSimpleClientset(), "sharko", "sharko-demo-pr-tracker")
	prTracker := prtracker.NewTracker(prCMStore, func() prtracker.GitProvider { return mockGit }, func(audit.Entry) {})
	for _, pr := range demoTrackedPRs(estate) {
		if trackErr := prTracker.TrackPR(context.Background(), pr); trackErr != nil {
			slog.Warn("demo: could not seed tracked PR", "pr_id", pr.PRID, "error", trackErr, "component", "demo")
		}
	}
	srv.SetPRTracker(prTracker)

	// 10. State-coverage wiring (maintainer scope addition, folded into
	// S1) — ONLY for a generated (non-default) estate, so the small
	// hand-written estate's behavior is completely unchanged. Two more
	// consumers read live state through a k8s client the demo previously
	// never provided at all:
	//
	//   - The cluster-secret reconciler (internal/clusterreconciler) is
	//     the sole holder of "is this cluster's ArgoCD-connection Secret
	//     labeled app.kubernetes.io/managed-by=sharko" — the signal
	//     behind models.Cluster.AlreadyManagedBySharko (owned vs
	//     "takeover-eligible") and the orphan-registration ownership
	//     gate. Constructed but never Started — no background reconcile
	//     loop runs; only ClientAndNamespace() is ever called, giving
	//     read access to the fake Secrets seeded below.
	//   - The observations store (internal/observations) persists the
	//     SharkoStatus 5-state model (Unknown/Connected/Verified/
	//     Operational/Unreachable), normally only reachable by manually
	//     clicking "Test connection" per cluster.
	//
	// Both are backed by ONE shared fake Kubernetes clientset, pre-seeded
	// with a Secret per cluster (labeled by default; see
	// estate.ForeignSecretClusterNames / MissingClusterNames /
	// ArgoOnlyClusters for the exceptions) — nothing here ever talks to a
	// real cluster.
	if estate != nil {
		demoK8s := k8sfake.NewSimpleClientset(buildDemoClusterSecrets(estate)...)

		clusterRecon := clusterreconciler.New(clusterreconciler.Deps{
			CMStore:     cmstore.NewStore(demoK8s, "sharko", "sharko-demo-cluster-reconciler"),
			GitProvider: func() gitprovider.GitProvider { return mockGit },
			ArgoClient:  demoK8s,
			Vault:       credProvider,
			AuditFn:     func(audit.Entry) {},
		})
		srv.SetClusterReconciler(clusterRecon)

		obsStore := observations.NewStore(demoK8s, "argocd")
		for _, seed := range estate.ObservationSeeds {
			result := verify.Result{Stage: seed.Stage, Success: seed.Success, DurationMs: 1200}
			if !seed.Success {
				result.ErrorCode = verify.ERR_NETWORK
				result.ErrorMessage = "dial tcp: connect: connection refused"
			}
			if seedErr := obsStore.RecordTestResult(context.Background(), seed.ClusterName, result); seedErr != nil {
				slog.Warn("demo: could not seed observation", "cluster", seed.ClusterName, "error", seedErr, "component", "demo")
			}
		}
		srv.SetObservationsStore(obsStore)

		// 11. Managed Secrets page (maintainer's ask, folded into S1) — the
		// page has real, believable data to show instead of 50x "unknown"
		// and zero addon-values rows. now is captured once here (server
		// start) and every timestamp below is relative to it — never a
		// fixed calendar date — so the page always reads as "just
		// happened" / "a few minutes ago" no matter when the demo is
		// actually run.
		now := time.Now()
		auditLog := srv.AuditLog()

		// Cluster connection secrets — a real state spread (S4), direct
		// seeding rather than an actual reconcile pass (see
		// buildDemoReconcileSeeds' doc comment for why).
		reconcileSeeds := buildDemoReconcileSeeds(estate)
		applyDemoReconcileSeeds(clusterRecon, reconcileSeeds, now)
		seedDemoConnectionRepairHistory(auditLog, reconcileSeeds, now)
		// A Refresh click (POST /clusters/{name}/reconcile) is a
		// fleet-wide nudge in production too (see handleReconcileCluster's
		// doc comment) — re-stamping every seeded record's last-checked
		// time to "now" on every trigger call is the honest demo
		// equivalent: it genuinely changes what the next read reports
		// (S3), without wiring up a background loop that would drift the
		// deterministic seed away from itself between server starts.
		srv.SetReconcilerTrigger(func() {
			applyDemoReconcileSeeds(clusterRecon, reconcileSeeds, time.Now())
		})
		// The Refresh path (P1-A A2) is the READ-ONLY check pass. Re-stamping
		// the same seeds with a fresh time is exactly what a check does in
		// demo: it looks again and writes down when it looked. No seed's
		// STATE moves, because a check never fixes what it finds — the demo
		// row that is out of sync stays out of sync until somebody clicks
		// Sync, which is the whole point of the two words being different.
		srv.SetReconcilerCheckTrigger(func() {
			applyDemoReconcileSeeds(clusterRecon, reconcileSeeds, time.Now())
		})

		// Addon values secrets — real rows across the generated estate
		// (S2), a full state spread including one row that has genuinely
		// never been checked (S3), and Refresh/Sync that actually change
		// what the next read reports.
		valuesRecon := newDemoAddonValuesReconciler(estate, addonSecretDefs, now, auditLog)
		// M5 (code review): same settings.Store the real engine's off switch
		// reads (SetEnabledFn/isEnabled — internal/secrets/reconciler.go),
		// wired here so `make demo-big`'s "Check all now" honestly refuses
		// once Settings -> Addon Values Engine is turned off, instead of
		// running regardless.
		valuesRecon.SetEnabledFn(demoSettingsStore.IsAddonValuesEngineEnabled)
		srv.SetSecretReconciler(valuesRecon)

		// 12. "Show me the actual Secret on the cluster" (S3). The read
		// itself goes through the real handler — including the real
		// server-side blanking — but the cluster it reads FROM is a fake
		// in-memory one per cluster, since demo mode has no real clusters
		// and its fake kubeconfigs point at addresses nothing answers. See
		// secret_resource_demo.go.
		srv.SetDemoRemoteClusterClient(newDemoRemoteClusterClients(valuesRecon, addonSecretDefs, now))
	}

	cleanup = func() {
		mockArgocd.Close()
	}
	return cleanup, nil
}

// buildDemoClusterSecrets renders one corev1.Secret per cluster the fake
// Kubernetes clientset needs for the cluster-reconciler's ownership checks
// (step 10 above) — named after the cluster in the "argocd" namespace,
// exactly like the real argosecrets-managed ArgoCD cluster Secret.
// Labeled app.kubernetes.io/managed-by=sharko by default; unlabeled
// ("foreign") for estate.ForeignSecretClusterNames and the
// not-SharkoOwned half of estate.ArgoOnlyClusters (the takeover-eligible
// and plain-not_in_git exemplars); omitted entirely for
// estate.MissingClusterNames (the "missing ArgoCD secret" exemplar).
func buildDemoClusterSecrets(estate *GeneratedEstate) []runtime.Object {
	missing := make(map[string]bool, len(estate.MissingClusterNames))
	for _, name := range estate.MissingClusterNames {
		missing[name] = true
	}
	foreign := make(map[string]bool, len(estate.ForeignSecretClusterNames))
	for _, name := range estate.ForeignSecretClusterNames {
		foreign[name] = true
	}

	// created spreads the fake creationTimestamps so the "show me the
	// resource" panel's age line reads as a real estate instead of every
	// secret being born in the same second. Relative to now, never a
	// calendar date.
	now := time.Now()
	index := 0

	newSecret := func(c Cluster, owned bool) *corev1.Secret {
		labels := map[string]string{}
		annotations := map[string]string{
			"sharko.io/managed-cluster": c.Name,
		}
		if owned {
			labels[clusterreconciler.LabelManagedBy] = clusterreconciler.LabelValueSharko
			labels["argocd.argoproj.io/secret-type"] = "cluster"
			// P2-C5/C4: provenance annotations on every Secret Sharko OWNS,
			// so the resource panel shows the same sharko.dev/* facts a
			// real deployment's writes would carry. Never on a secret
			// Sharko does NOT own (owned==false — self-managed/foreign),
			// matching the real write path's rule.
			annotations[clusterreconciler.AnnotationSourceFile] = demoComparedPath(index)
			annotations[clusterreconciler.AnnotationRevision] = demoBranchHeadSHA
			annotations[clusterreconciler.AnnotationWrittenAt] = now.Add(-demoSecretAgeOffsets[index%len(demoSecretAgeOffsets)]).UTC().Format(time.RFC3339)
		}
		// The addon labels are the genuinely useful content of a
		// connection secret — they are what decides which addons run on
		// this cluster, and they are not secret. Rendering them here means
		// the demo's resource panel shows the same thing a real one does.
		for addonName := range c.Addons {
			labels[addonName] = "enabled"
		}
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:              c.Name,
				Namespace:         "argocd",
				Labels:            labels,
				Annotations:       annotations,
				CreationTimestamp: metav1.NewTime(now.Add(-demoSecretAgeOffsets[index%len(demoSecretAgeOffsets)])),
			},
			Data: map[string][]byte{
				"name":   []byte(c.Name),
				"server": []byte("https://k8s." + c.Name + ".demo.internal"),
				// A real ArgoCD cluster secret keeps the bearer token
				// inside data["config"]. The demo carries the key so the
				// resource panel shows what an operator would actually
				// see — blanked, exactly like the real one.
				"config": []byte(`{"bearerToken":"` + demoSecretValue + `","tlsClientConfig":{"insecure":true}}`),
			},
			Type: corev1.SecretTypeOpaque,
		}
		index++
		return sec
	}

	objs := make([]runtime.Object, 0, len(estate.Clusters)+len(estate.ArgoOnlyClusters))
	for _, c := range estate.Clusters {
		if missing[c.Name] {
			continue
		}
		objs = append(objs, newSecret(c, !foreign[c.Name]))
	}
	for _, ao := range estate.ArgoOnlyClusters {
		objs = append(objs, newSecret(ao.Cluster, ao.SharkoOwned))
	}
	return objs
}

// demoTrackedPRs returns the PR-tracker feed for /api/v1/prs. For a
// generated estate it's estate.TrackedPRs (S1's dozens-of-PRs feature). For
// the default (nil estate) size it mirrors the 2 PRs mock_git.go seeds
// directly into the mock Git provider (PR 41 the staging-eu cert-manager
// upgrade, PR 42 the dr-eu registration) so the small estate's PR panel
// isn't empty either — a fix, not a change to the fixture's shape.
func demoTrackedPRs(estate *GeneratedEstate) []prtracker.PRInfo {
	if estate != nil {
		return estate.TrackedPRs
	}
	return []prtracker.PRInfo{
		{
			PRID:       41,
			PRUrl:      "https://github.com/demo/sharko-addons/pull/41",
			PRBranch:   "sharko/upgrade-cert-manager-staging-eu",
			PRTitle:    "sharko: upgrade cert-manager 1.13.6 → 1.14.4 on staging-eu",
			PRBase:     "main",
			Cluster:    "staging-eu",
			Addon:      "cert-manager",
			Operation:  prtracker.OpAddonUpgrade,
			User:       "sharko-bot",
			Source:     "ui",
			CreatedAt:  time.Date(2025, 1, 18, 9, 0, 0, 0, time.UTC),
			LastStatus: "open",
			LastPolled: time.Date(2025, 1, 18, 9, 0, 0, 0, time.UTC),
		},
		{
			PRID:       42,
			PRUrl:      "https://github.com/demo/sharko-addons/pull/42",
			PRBranch:   "sharko/register-dr-eu",
			PRTitle:    "sharko: register cluster dr-eu",
			PRBase:     "main",
			Cluster:    "dr-eu",
			Operation:  prtracker.OpRegisterCluster,
			User:       "sharko-bot",
			Source:     "ui",
			CreatedAt:  time.Date(2025, 1, 19, 14, 22, 0, 0, time.UTC),
			LastStatus: "open",
			LastPolled: time.Date(2025, 1, 19, 14, 22, 0, 0, time.UTC),
		},
	}
}

// --- In-memory config.Store implementation ---

// inMemoryStore implements config.Store entirely in memory.
// Used by the demo setup to avoid touching the filesystem.
type inMemoryStore struct {
	mu               sync.RWMutex
	connections      []models.Connection
	activeConnection string
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{}
}

func (s *inMemoryStore) ListConnections() ([]models.Connection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]models.Connection, len(s.connections))
	copy(result, s.connections)
	return result, nil
}

func (s *inMemoryStore) GetConnection(name string) (*models.Connection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.connections {
		if s.connections[i].Name == name {
			c := s.connections[i]
			return &c, nil
		}
	}
	return nil, nil
}

func (s *inMemoryStore) SaveConnection(conn models.Connection) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.connections {
		if s.connections[i].Name == conn.Name {
			s.connections[i] = conn
			return nil
		}
	}
	s.connections = append(s.connections, conn)

	// First connection is auto-active.
	if len(s.connections) == 1 {
		s.activeConnection = conn.Name
	}
	return nil
}

func (s *inMemoryStore) DeleteConnection(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	filtered := s.connections[:0]
	for _, c := range s.connections {
		if c.Name != name {
			filtered = append(filtered, c)
		}
	}
	s.connections = filtered
	if s.activeConnection == name {
		s.activeConnection = ""
		if len(s.connections) > 0 {
			s.activeConnection = s.connections[0].Name
		}
	}
	return nil
}

func (s *inMemoryStore) GetActiveConnection() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.activeConnection != "" {
		return s.activeConnection, nil
	}
	if len(s.connections) > 0 {
		return s.connections[0].Name, nil
	}
	return "", nil
}

func (s *inMemoryStore) SetActiveConnection(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeConnection = name
	return nil
}

func (s *inMemoryStore) MergeConnectionFromEnvAtomic(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find the connection
	var conn *models.Connection
	for i := range s.connections {
		if s.connections[i].Name == name {
			conn = &s.connections[i]
			break
		}
	}
	if conn == nil {
		return false, nil
	}

	// Merge non-secret env fields onto the fresh load
	if !config.MergeConnectionFromEnv(conn) {
		return false, nil
	}

	return true, nil
}
