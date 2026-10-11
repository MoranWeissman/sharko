package verify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// These tests read the fake clientset's action log, so they can say exactly
// which writes reached the cluster: "zero writes on a preview", and "the
// namespace is deleted only when this run created it".

// writeActions returns every write the fake clientset saw, as
// "<verb> <resource> <namespace-or-name>". Reads (get, list, watch) are left
// out on purpose: a preview is allowed to read.
func writeActions(client *fake.Clientset) []string {
	var out []string
	for _, a := range client.Actions() {
		switch a.GetVerb() {
		case "get", "list", "watch":
			continue
		}
		target := a.GetNamespace()
		if a.GetResource().Resource == "namespaces" {
			switch act := a.(type) {
			case k8stesting.CreateAction:
				if ns, ok := act.GetObject().(*corev1.Namespace); ok {
					target = ns.Name
				}
			case k8stesting.DeleteAction:
				target = act.GetName()
			}
		}
		out = append(out, fmt.Sprintf("%s %s %s", a.GetVerb(), a.GetResource().Resource, target))
	}
	return out
}

func assertWrites(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes on the cluster:\n  got  %q\n  want %q", got, want)
	}
}

func existingNamespace() *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}}
}

// ── preview: zero writes ───────────────────────────────────────────────

func TestStage1Preview_MakesNoWrites_NamespaceMissing(t *testing.T) {
	client := fake.NewSimpleClientset()

	result := Stage1Preview(context.Background(), client, "sharko-test")
	if !result.Success {
		t.Fatalf("a reachable cluster should pass the preview check, got %s: %s", result.ErrorCode, result.ErrorMessage)
	}
	assertWrites(t, writeActions(client), nil)

	if _, err := client.CoreV1().Namespaces().Get(context.Background(), "sharko-test", metav1.GetOptions{}); err == nil {
		t.Fatal("the preview created the test namespace; a preview must not write")
	}
}

func TestStage1Preview_MakesNoWrites_NamespaceExists(t *testing.T) {
	client := fake.NewSimpleClientset(existingNamespace())

	result := Stage1Preview(context.Background(), client, "sharko-test")
	if !result.Success {
		t.Fatalf("expected success, got %s: %s", result.ErrorCode, result.ErrorMessage)
	}
	assertWrites(t, writeActions(client), nil)
}

// The preview still READS: the server version and the namespace. That is
// how it can say whether the cluster answers at all.
func TestStage1Preview_StillReadsVersionAndNamespace(t *testing.T) {
	client := fake.NewSimpleClientset(existingNamespace())

	result := Stage1Preview(context.Background(), client, "sharko-test")

	sawNamespaceGet := false
	for _, a := range client.Actions() {
		if a.GetVerb() == "get" && a.GetResource().Resource == "namespaces" {
			sawNamespaceGet = true
		}
	}
	if !sawNamespaceGet {
		t.Error("the preview did not look the test namespace up")
	}
	if result.Steps[0].Name != "Fetch server version" || result.Steps[0].Status != "pass" {
		t.Errorf("first step = %+v, want a passing server-version read", result.Steps[0])
	}
}

// The preview says, by exact text, what it did not check.
func TestStage1Preview_SaysWriteAccessWasNotChecked(t *testing.T) {
	if PreviewNotCheckedSentence != "Write access was not checked because this is a preview." {
		t.Fatalf("PreviewNotCheckedSentence changed: %q", PreviewNotCheckedSentence)
	}

	client := fake.NewSimpleClientset()
	result := Stage1Preview(context.Background(), client, "sharko-test")

	if len(result.Notes) != 1 || result.Notes[0] != PreviewNotCheckedSentence {
		t.Fatalf("Notes = %q, want exactly [%q]", result.Notes, PreviewNotCheckedSentence)
	}

	wantSkipped := map[string]bool{"Create test secret": true, "Read back test secret": true, "Delete test secret": true}
	for _, step := range result.Steps {
		if !wantSkipped[step.Name] {
			continue
		}
		delete(wantSkipped, step.Name)
		if step.Status != "skipped" || step.Detail != PreviewNotCheckedSentence {
			t.Errorf("step %q = %+v, want skipped with the preview sentence", step.Name, step)
		}
	}
	if len(wantSkipped) != 0 {
		t.Errorf("preview steps are missing %v", wantSkipped)
	}
}

// A preview against a cluster that does not answer still fails, still writes
// nothing, and still says what was not checked.
func TestStage1Preview_UnreachableFailsWithoutWrites(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("get", "namespaces", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("namespaces is forbidden: cannot get namespaces")
	})

	result := Stage1Preview(context.Background(), client, "sharko-test")
	if result.Success {
		t.Fatal("expected the preview to fail when the namespace read is refused")
	}
	if result.ErrorCode != ERR_RBAC {
		t.Errorf("error code = %s, want ERR_RBAC", result.ErrorCode)
	}
	assertWrites(t, writeActions(client), nil)
	if len(result.Notes) != 1 || result.Notes[0] != PreviewNotCheckedSentence {
		t.Errorf("Notes = %q, want the preview sentence", result.Notes)
	}
}

// ── full check: cleans up, and never deletes a namespace it did not make ──

func TestStage1_NamespaceAlreadyThere_IsNeverDeleted(t *testing.T) {
	client := fake.NewSimpleClientset(existingNamespace())

	result := Stage1(context.Background(), client, "sharko-test")
	if !result.Success {
		t.Fatalf("expected success, got %s: %s", result.ErrorCode, result.ErrorMessage)
	}
	assertWrites(t, writeActions(client), []string{
		"create secrets sharko-test",
		"delete secrets sharko-test",
	})
	if _, err := client.CoreV1().Namespaces().Get(context.Background(), "sharko-test", metav1.GetOptions{}); err != nil {
		t.Fatalf("a namespace that was already there must never be deleted: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("unexpected warnings: %q", result.Warnings)
	}
}

// A namespace that was already there stays even when the check FAILS part
// way through.
func TestStage1_NamespaceAlreadyThere_NotDeletedOnFailure(t *testing.T) {
	client := fake.NewSimpleClientset(existingNamespace())
	client.PrependReactor("get", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("secrets is forbidden: cannot get secrets")
	})

	result := Stage1(context.Background(), client, "sharko-test")
	if result.Success {
		t.Fatal("expected failure when the read-back is refused")
	}
	for _, w := range writeActions(client) {
		if strings.HasPrefix(w, "delete namespaces") {
			t.Fatalf("Stage1 deleted a namespace it did not create: %q", writeActions(client))
		}
	}
}

func TestStage1_NamespaceCreated_LabelledAsSharkos(t *testing.T) {
	client := fake.NewSimpleClientset()
	var created *corev1.Namespace
	client.PrependReactor("create", "namespaces", func(a k8stesting.Action) (bool, runtime.Object, error) {
		created = a.(k8stesting.CreateAction).GetObject().(*corev1.Namespace).DeepCopy()
		return false, nil, nil
	})

	result := Stage1(context.Background(), client, "sharko-test")
	if !result.Success {
		t.Fatalf("expected success, got %s", result.ErrorCode)
	}
	if created == nil || created.Labels["app.kubernetes.io/managed-by"] != "sharko" {
		t.Fatalf("the namespace Sharko creates must carry the managed-by label, got %+v", created)
	}
	if got := result.Steps[1].Detail; got != "created for this test, then removed" {
		t.Errorf("Ensure namespace detail = %q, want %q", got, "created for this test, then removed")
	}
}

// The read-back fails after the Secret was made. The old code returned here
// and left the Secret behind. Now it is removed on the way out.
func TestStage1_ReadBackFails_SecretStillRemoved(t *testing.T) {
	client := fake.NewSimpleClientset(existingNamespace())
	client.PrependReactor("get", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("secrets is forbidden: cannot get secrets")
	})

	result := Stage1(context.Background(), client, "sharko-test")
	if result.Success {
		t.Fatal("expected failure")
	}
	assertWrites(t, writeActions(client), []string{
		"create secrets sharko-test",
		"delete secrets sharko-test",
	})
	list, _ := client.CoreV1().Secrets("sharko-test").List(context.Background(), metav1.ListOptions{})
	if len(list.Items) != 0 {
		t.Fatalf("a test Secret was left behind: %d", len(list.Items))
	}
}

// A namespace this run created is removed even when the check fails.
func TestStage1_CreatedNamespace_RemovedOnFailure(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("secrets is forbidden: cannot create secrets")
	})

	result := Stage1(context.Background(), client, "sharko-test")
	if result.Success {
		t.Fatal("expected failure")
	}
	assertWrites(t, writeActions(client), []string{
		"create namespaces sharko-test",
		"create secrets sharko-test",
		"delete namespaces sharko-test",
	})
}

// A cleanup that fails keeps a passing check passing, and says so in a
// warning (Sharko's own sentence, nothing the cluster said).
func TestStage1_CleanupFailure_IsAWarningNotAFailure(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("delete", "namespaces", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("namespaces \"sharko-test\" is forbidden: SECRET-HOSTNAME-10.9.9.9 cannot delete")
	})

	result := Stage1(context.Background(), client, "sharko-test")
	if !result.Success {
		t.Fatalf("a cleanup failure must not fail the check, got %s", result.ErrorCode)
	}
	want := `Sharko created the namespace "sharko-test" for this test but could not remove it afterwards. You can delete it by hand.`
	if len(result.Warnings) != 1 || result.Warnings[0] != want {
		t.Fatalf("Warnings = %q, want [%q]", result.Warnings, want)
	}
	if strings.Contains(strings.Join(result.Warnings, " "), "SECRET-HOSTNAME") {
		t.Fatal("a warning carries the cluster's own words")
	}
	if got := result.Steps[1].Detail; got != "created" {
		t.Errorf("Ensure namespace detail = %q; it must not claim the namespace was removed", got)
	}
}

func TestStage1_SecretCleanupFailure_InExistingNamespace_IsAWarning(t *testing.T) {
	client := fake.NewSimpleClientset(existingNamespace())
	client.PrependReactor("get", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("secrets is forbidden: cannot get secrets")
	})
	client.PrependReactor("delete", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("secrets is forbidden: cannot delete secrets")
	})

	result := Stage1(context.Background(), client, "sharko-test")
	if result.Success {
		t.Fatal("expected failure from the read-back")
	}
	if result.ErrorCode != ERR_RBAC {
		t.Errorf("error code = %s; the cleanup must not change the reason the check failed", result.ErrorCode)
	}
	if len(result.Warnings) != 1 || !strings.HasPrefix(result.Warnings[0], "Sharko could not remove its test Secret ") ||
		!strings.HasSuffix(result.Warnings[0], ` from namespace "sharko-test". It holds no real data, and you can delete it by hand.`) {
		t.Fatalf("Warnings = %q", result.Warnings)
	}
}

// A namespace that is still being deleted (by the cleanup of a test a few
// seconds ago) is waited out and made again, instead of failing the check.
func TestStage1_TerminatingNamespace_IsWaitedOut(t *testing.T) {
	ns := existingNamespace()
	ns.Status.Phase = corev1.NamespaceTerminating
	client := fake.NewSimpleClientset(ns)

	gets := 0
	client.PrependReactor("get", "namespaces", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		if gets == 2 {
			// The namespace finishes going away between the first and the
			// second look.
			_ = client.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("namespaces"), "", "sharko-test")
		}
		return false, nil, nil
	})

	result := Stage1(context.Background(), client, "sharko-test")
	if !result.Success {
		t.Fatalf("expected success after waiting for the old namespace, got %s", result.ErrorCode)
	}
	assertWrites(t, writeActions(client), []string{
		"create namespaces sharko-test",
		"create secrets sharko-test",
		"delete secrets sharko-test",
		"delete namespaces sharko-test",
	})
}
