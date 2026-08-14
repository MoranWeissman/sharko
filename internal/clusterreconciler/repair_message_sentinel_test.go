package clusterreconciler

// repair_message_sentinel_test.go — R4-2. The reworded repair messages say
// something true, and they still say nothing about the credential itself.
//
// Rewording a success message is exactly the moment a value can slip into it, so
// the words and the sentinel are checked together rather than in two rounds.
// One unique fake credential goes through a real full repair, and every surface
// that repair produces is searched for it: the per-cluster record, the audit
// entries, the Kubernetes event, the server logs and the reported field paths.
//
// Raw, base64 and SHA-256 forms, plus prefix and suffix fragments. And no
// length, because a length is information about a credential.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/events"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// repairMessageSentinel is a made-up credential used only in this file, and
// nowhere else in the repository — so a hit anywhere can only have come from
// here.
const repairMessageSentinel = "wq7f-repair-message-sentinel-never-ship-31908"

// TestFullRepairSurfacesSayWhatHappenedAndCarryNoCredential drives a real full
// repair and checks both halves at once: the messages claim what a full repair
// actually does, and nothing anywhere carries the credential.
func TestFullRepairSurfacesSayWhatHappenedAndCarryNoCredential(t *testing.T) {
	// Capture server logs for the duration of the repair.
	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevLogger) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	live := ownedConnSecret(
		map[string]string{"datadog": "disabled"},
		map[string]string{
			"name":   repairReconCluster,
			"server": "https://stale.invalid",
			"config": `{"bearerToken":"an-older-made-up-value"}`,
		},
		nil)
	client := fake.NewSimpleClientset(live)
	audits := &auditCollector{}
	fakeRecorder := record.NewFakeRecorder(20)

	fg := &fakeGit{files: map[string][]byte{}}
	r := New(Deps{
		GitProvider:   func() gitprovider.GitProvider { return fg },
		ArgoClient:    client,
		Vault:         staticVault(&fakeVault{}),
		AuditFn:       audits.Add,
		EventRecorder: events.NewRecorderForTest(fakeRecorder, "sharko"),
		TickInterval:  0,
	})

	desired, buildErr := argosecrets.BuildClusterSecret(argosecrets.ClusterSecretSpec{
		Name:   repairReconCluster,
		Server: "https://" + repairReconCluster + ".invalid",
		Token:  repairMessageSentinel,
		Labels: map[string]string{"datadog": "enabled"},
	}, "argocd")
	if buildErr != nil {
		t.Fatalf("building the desired Secret: %v", buildErr)
	}

	res, err := r.RepairOwnedConnectionSecret(context.Background(), desired, "cafe1234")
	if err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if !res.Changed {
		t.Fatal("the repair reported no change, but the connection genuinely drifted — every assertion below would prove nothing")
	}
	r.EmitConnectionRepairEvent(repairReconCluster, len(res.FieldsWritten))

	// --- 1. The per-cluster record says something true. ---------------------
	rec, ok := r.LastReconcile(repairReconCluster)
	if !ok {
		t.Fatal("the repair recorded no per-cluster outcome")
	}
	if strings.Contains(rec.Message, "stored sign-in details") {
		t.Errorf(`the per-cluster record says the repair rewrote the STORED sign-in details: %q.

For an EKS connection the backend stores cluster metadata and the credential is created at write time, so nothing stored was rewritten. Name the configured credentials source instead — that is true for both kinds of connection.`, rec.Message)
	}
	if !strings.Contains(rec.Message, "configured credentials source") {
		t.Errorf("the per-cluster record does not name the cluster's configured credentials source: %q", rec.Message)
	}

	// --- 2. The event says something true. ---------------------------------
	var event string
	select {
	case event = <-fakeRecorder.Events:
	default:
		t.Fatal("EmitConnectionRepairEvent emitted nothing")
	}
	if strings.Contains(event, "stored sign-in details") {
		t.Errorf("the repair event says the STORED sign-in details were rewritten: %q", event)
	}
	if !strings.Contains(event, "configured credentials source") {
		t.Errorf("the repair event does not name the configured credentials source: %q", event)
	}

	// --- 3. Nothing carries the credential, in any form. --------------------
	auditBlob := ""
	for _, e := range audits.Snapshot() {
		auditBlob += e.Event + e.Resource + e.Detail + e.Error + e.User + e.Action + e.Result
	}
	surfaces := map[string]string{
		"the per-cluster record":   rec.Message,
		"the Kubernetes event":     event,
		"the audit entries":        auditBlob,
		"the server logs":          logBuf.String(),
		"the reported field paths": strings.Join(res.FieldsWritten, " "),
		"the applied revision":     res.AppliedRevision,
	}

	sum := sha256.Sum256([]byte(repairMessageSentinel))
	forms := map[string]string{
		"raw":            repairMessageSentinel,
		"base64":         base64.StdEncoding.EncodeToString([]byte(repairMessageSentinel)),
		"base64 raw-url": base64.RawURLEncoding.EncodeToString([]byte(repairMessageSentinel)),
		"sha-256 hex":    hex.EncodeToString(sum[:]),
		"sha-256 base64": base64.StdEncoding.EncodeToString(sum[:]),
		"prefix":         repairMessageSentinel[:14],
		"suffix":         repairMessageSentinel[len(repairMessageSentinel)-14:],
	}

	for surfaceName, surface := range surfaces {
		for form, needle := range forms {
			if strings.Contains(surface, needle) {
				t.Errorf("%s carries the credential in %s form", surfaceName, form)
			}
		}
	}

	// --- 4. No length anywhere either. -------------------------------------
	//
	// Checked on the two surfaces a sentence is composed into. The logs and the
	// audit detail carry field COUNTS, and a small count could coincidentally
	// equal a length, so those are left out rather than made flaky — the
	// sentinel is long enough that its length cannot be confused with a field
	// count on the two surfaces that matter.
	lengths := []string{
		strconv.Itoa(len(repairMessageSentinel)),
		strconv.Itoa(len(`{"bearerToken":"` + repairMessageSentinel + `"}`)),
	}
	for _, surfaceName := range []string{"the per-cluster record", "the Kubernetes event"} {
		for _, n := range lengths {
			if strings.Contains(surfaces[surfaceName], n) {
				t.Errorf(`%s contains %q, which is a length of the credential. Lengths are never reported.`, surfaceName, n)
			}
		}
	}
}
