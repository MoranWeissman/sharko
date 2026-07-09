package clusterreconciler

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// V2-cleanup-89.5 — label-fight detection on self-managed connections.
//
// When a self-managed connection's ArgoCD cluster Secret is ALSO rendered
// from Git by another ArgoCD Application, that Application's sync (e.g.
// syncOptions: [Replace=true], or self-heal against a conflicting manifest
// value) can revert the addon labels Sharko writes every tick, producing a
// silent fight. These tests pin:
//
//  1. No revert → no warning, ever.
//  2. A single reverted tick is not enough — the warning only surfaces
//     after fightRevertThreshold (2) CONSECUTIVE reverted ticks.
//  3. A non-reverted tick resets the streak — an isolated revert followed
//     by a clean tick must not accumulate toward the next revert.
//  4. Git itself changing the desired value between ticks is NOT a revert
//     — an ordinary addon toggle must never trip the warning.
//  5. The outcome stays Succeeded even while warning — Sharko keeps
//     re-applying its labels; the warning is visibility, not a behavior
//     change.

// revertLiveLabel simulates another actor (e.g. the ArgoCD Application
// that renders this Secret from Git) changing a label on the live Secret
// between reconciler ticks — the exact scenario recordFightCheck must
// detect.
func revertLiveLabel(t *testing.T, client *fake.Clientset, name, key, value string) {
	t.Helper()
	ctx := context.Background()
	sec, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("revertLiveLabel: get %q: %v", name, err)
	}
	sec = sec.DeepCopy()
	if sec.Labels == nil {
		sec.Labels = map[string]string{}
	}
	sec.Labels[key] = value
	if _, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Update(ctx, sec, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("revertLiveLabel: update %q: %v", name, err)
	}
}

// TestFightDetection_NoRevert_NoWarning — the ordinary case: nothing
// touches the Secret between ticks, so no revert is ever observed and
// Message stays empty across many ticks.
func TestFightDetection_NoRevert_NoWarning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := fake.NewSimpleClientset(userSecret("user-cluster", nil))
	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, &auditCollector{}, body)

	for i := 0; i < 4; i++ {
		r.pollOnce(ctx)
		rec, ok := r.LastReconcile("user-cluster")
		if !ok {
			t.Fatalf("tick %d: expected a LastReconcile record", i)
		}
		if rec.Outcome != OutcomeSucceeded {
			t.Fatalf("tick %d: Outcome = %q, want %q", i, rec.Outcome, OutcomeSucceeded)
		}
		if rec.Message != "" {
			t.Fatalf("tick %d: Message = %q, want empty (no revert ever happened)", i, rec.Message)
		}
	}
}

// TestFightDetection_SingleRevert_BelowThreshold_NoWarning — one reverted
// tick alone must not surface a warning; a single unlucky race is not a
// sustained fight.
func TestFightDetection_SingleRevert_BelowThreshold_NoWarning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := fake.NewSimpleClientset(userSecret("user-cluster", nil))
	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, &auditCollector{}, body)

	r.pollOnce(ctx) // tick 1: writes addon-foo=enabled, establishes the baseline
	first, _ := r.LastReconcile("user-cluster")
	if first.Message != "" {
		t.Fatalf("tick 1: Message = %q, want empty", first.Message)
	}

	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")

	r.pollOnce(ctx) // tick 2: sees the revert, but streak=1 < threshold
	second, ok := r.LastReconcile("user-cluster")
	if !ok {
		t.Fatal("tick 2: expected a LastReconcile record")
	}
	if second.Outcome != OutcomeSucceeded {
		t.Fatalf("tick 2: Outcome = %q, want %q (still re-applying, not failing)", second.Outcome, OutcomeSucceeded)
	}
	if second.Message != "" {
		t.Fatalf("tick 2: Message = %q, want empty (only 1 revert, below threshold)", second.Message)
	}
}

// TestFightDetection_TwoConsecutiveReverts_WarningSurfaces — the positive
// case: two reverted ticks IN A ROW surface the fight warning, naming the
// pattern without failing the reconcile outcome.
func TestFightDetection_TwoConsecutiveReverts_WarningSurfaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := fake.NewSimpleClientset(userSecret("user-cluster", nil))
	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, &auditCollector{}, body)

	r.pollOnce(ctx) // tick 1: establishes baseline

	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 2: revert #1 — still below threshold

	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 3: revert #2 — threshold reached

	rec, ok := r.LastReconcile("user-cluster")
	if !ok {
		t.Fatal("tick 3: expected a LastReconcile record")
	}
	if rec.Outcome != OutcomeSucceeded {
		t.Fatalf("tick 3: Outcome = %q, want %q (Sharko is still successfully re-applying labels)", rec.Outcome, OutcomeSucceeded)
	}
	if rec.Message == "" {
		t.Fatal("tick 3: expected a non-empty fight warning after 2 consecutive reverts")
	}
	if !strings.Contains(rec.Message, "overwriting") {
		t.Errorf("Message = %q, want it to describe the overwrite pattern", rec.Message)
	}

	// Sharko must still have re-applied its label this tick — the warning
	// is visibility, not a behavior change.
	sec, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "user-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if sec.Labels["addon-foo"] != "enabled" {
		t.Fatalf("addon-foo = %q, want %q — Sharko must keep re-applying its label every tick", sec.Labels["addon-foo"], "enabled")
	}
}

// TestFightDetection_ResetOnCleanTick — a clean (non-reverted) tick resets
// the streak: revert, clean, revert must NOT reach the threshold on the
// second isolated revert.
func TestFightDetection_ResetOnCleanTick(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := fake.NewSimpleClientset(userSecret("user-cluster", nil))
	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, &auditCollector{}, body)

	r.pollOnce(ctx) // tick 1: baseline

	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 2: revert #1 (streak=1)

	r.pollOnce(ctx) // tick 3: clean — Sharko's tick 2 write is still live, streak resets to 0

	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 4: revert #1 again (streak=1, NOT 2 — reset worked)

	rec, ok := r.LastReconcile("user-cluster")
	if !ok {
		t.Fatal("tick 4: expected a LastReconcile record")
	}
	if rec.Message != "" {
		t.Fatalf("tick 4: Message = %q, want empty — the clean tick 3 must have reset the streak", rec.Message)
	}
}

// TestFightDetection_GitDrivenChange_NotAFalsePositive — the critical
// false-positive guard: when GIT changes what Sharko wants for a key
// between ticks, the live value differing from the OLD desired value is
// expected (Sharko itself is about to change it) and must never count as
// a revert, no matter how many ticks in a row an addon gets toggled.
func TestFightDetection_GitDrivenChange_NotAFalsePositive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fg := &fakeGit{files: map[string][]byte{
		DefaultManagedClustersPath: envelopedWithModes(testClusterEntry{
			Name:   "user-cluster",
			Mode:   "user",
			Labels: map[string]string{"addon-foo": "enabled"},
		}),
	}}
	k8sClient := fake.NewSimpleClientset(userSecret("user-cluster", nil))
	r := newReconcilerForTest(t, fg, k8sClient, &fakeVault{}, &auditCollector{}, nil)

	r.pollOnce(ctx) // tick 1: addon-foo=enabled written, baseline set

	// Git toggles the addon off. The live Secret still holds "enabled"
	// (Sharko's own tick-1 write) — this MUST NOT be flagged as a revert,
	// even though it differs from what git wants now.
	fg.files[DefaultManagedClustersPath] = envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "disabled"},
	})
	r.pollOnce(ctx) // tick 2: git-driven change

	rec, ok := r.LastReconcile("user-cluster")
	if !ok {
		t.Fatal("tick 2: expected a LastReconcile record")
	}
	if rec.Message != "" {
		t.Fatalf("tick 2: Message = %q, want empty — a git-driven change must never look like a revert", rec.Message)
	}

	// Toggle back on for a third tick — still no false positive across
	// repeated ordinary toggles.
	fg.files[DefaultManagedClustersPath] = envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	r.pollOnce(ctx) // tick 3

	rec, ok = r.LastReconcile("user-cluster")
	if !ok {
		t.Fatal("tick 3: expected a LastReconcile record")
	}
	if rec.Message != "" {
		t.Fatalf("tick 3: Message = %q, want empty", rec.Message)
	}
}
