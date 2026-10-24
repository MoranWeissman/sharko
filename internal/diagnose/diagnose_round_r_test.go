package diagnose

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"
)

// EP3 dry run round R, R12.

func failedChecks(r *DiagnosticReport) []string {
	var out []string
	for _, c := range r.NamespaceAccess {
		if !c.Passed {
			out = append(out, c.Permission+": "+c.Error)
		}
	}
	return out
}

// A namespace an earlier check is still deleting is waited out, then made
// again: no false "missing right" and no fix.
func TestDiagnose_NamespaceStillBeingDeleted_IsWaitedOut(t *testing.T) {
	client := fake.NewSimpleClientset()
	var gets int32
	client.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		if atomic.AddInt32(&gets, 1) == 1 {
			return true, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"},
				Status: corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating}}, nil
		}
		return true, nil, errors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, "sharko-test")
	})
	report := DiagnoseCluster(context.Background(), client, "sharko-test", "spoke-1", "N/A")
	if f := failedChecks(report); len(f) != 0 || len(report.SuggestedFixes) != 0 {
		t.Fatalf("a namespace being deleted gave failures %v and fixes %+v", f, report.SuggestedFixes)
	}
	if atomic.LoadInt32(&gets) < 2 {
		t.Fatal("the check did not wait for the namespace to go")
	}
}

// When it is still there after the wait, the check says so in plain words
// and suggests no RBAC change.
func TestDiagnose_NamespaceStillBeingDeletedAfterTheWait_SaysSoWithNoFix(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating}}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the wait ends at once
	report := DiagnoseCluster(ctx, client, "sharko-test", "spoke-1", "N/A")
	want := "namespace sharko-test ready: The test namespace sharko-test is still being deleted, from an earlier check. Wait a minute, then check again."
	if f := failedChecks(report); len(f) != 1 || f[0] != want {
		t.Fatalf("failures = %q, want [%q]", f, want)
	}
	if len(report.SuggestedFixes) != 0 {
		t.Fatalf("a namespace being deleted is not a missing right: %+v", report.SuggestedFixes)
	}
}

// A test Secret left over from an earlier run counts as "can create", and
// the get and delete then clean it up.
func TestDiagnose_LeftoverTestSecret_IsCleanedUp(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: testSecretName, Namespace: "sharko-test"}},
	)
	report := DiagnoseCluster(context.Background(), client, "sharko-test", "spoke-1", "N/A")
	if f := failedChecks(report); len(f) != 0 || len(report.SuggestedFixes) != 0 {
		t.Fatalf("a leftover test Secret gave failures %v", f)
	}
	if _, err := client.CoreV1().Secrets("sharko-test").Get(context.Background(), testSecretName, metav1.GetOptions{}); !errors.IsNotFound(err) {
		t.Fatalf("the leftover test Secret is still there: %v", err)
	}
}

// The account name is quoted, so a name with ": " or "#" in it is still
// valid YAML and reads back as the same name.
func TestFix_AccountNameIsQuotedYAML(t *testing.T) {
	name := "arn:aws:sts::1:assumed-role/x: y #z"
	fix := generateFix("list namespaces", "x", name)
	docs := strings.Split(fix.YAML, "\n---\n")
	var binding struct {
		Subjects []struct {
			Name string `json:"name"`
		} `json:"subjects"`
	}
	if err := yaml.Unmarshal([]byte(docs[1]), &binding); err != nil {
		t.Fatalf("the fix is not valid YAML: %v\n%s", err, fix.YAML)
	}
	if len(binding.Subjects) != 1 || binding.Subjects[0].Name != name {
		t.Fatalf("subjects = %+v, want the name %q", binding.Subjects, name)
	}
}

// Two fixes with the same YAML but different reasons keep both reasons.
func TestAddFix_SameYAMLKeepsBothReasons(t *testing.T) {
	fixes := addFix(nil, Fix{Description: "Reason one.", YAML: "same"})
	fixes = addFix(fixes, Fix{Description: "Reason two.", YAML: "same"})
	fixes = addFix(fixes, Fix{Description: "Reason one.", YAML: "same"})
	fixes = addFix(fixes, Fix{Description: "Other.", YAML: "other"})
	if len(fixes) != 2 || fixes[0].Description != "Reason one. Reason two." || fixes[1].Description != "Other." {
		t.Fatalf("fixes = %+v", fixes)
	}
}

// The namespace this run made is removed even when the run stops early.
func TestDiagnose_MadeNamespaceIsRemovedOnEveryPath(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authv1.SelfSubjectReview{}, nil
	})
	client.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.NewForbidden(schema.GroupResource{Resource: "secrets"}, testSecretName, nil)
	})
	DiagnoseCluster(context.Background(), client, "sharko-test", "spoke-1", "N/A")
	if _, err := client.CoreV1().Namespaces().Get(context.Background(), "sharko-test", metav1.GetOptions{}); !errors.IsNotFound(err) {
		t.Fatalf("the namespace this run made is still there: %v", err)
	}
}

// EP3 dry run round S, S16: only a namespace still being deleted when the
// wait ends is "still being deleted".

// A read that fails during the wait is that failure, with its own fix.
func TestDiagnose_ReadFailsDuringTheWait_IsThatFailureNotStillDeleting(t *testing.T) {
	client := fake.NewSimpleClientset()
	var gets int32
	client.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		if atomic.AddInt32(&gets, 1) == 1 {
			return true, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"},
				Status: corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating}}, nil
		}
		return true, nil, errors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "sharko-test", nil)
	})
	report := DiagnoseCluster(context.Background(), client, "sharko-test", "spoke-1", "N/A")
	f := failedChecks(report)
	if len(f) == 0 {
		t.Fatal("a refused read gave no failure")
	}
	for _, line := range f {
		if strings.Contains(line, "still being deleted") {
			t.Fatalf("a refused read was reported as still being deleted: %q", f)
		}
	}
	if !strings.HasPrefix(f[0], "get namespace sharko-test: ") {
		t.Fatalf("failures = %q, want the get namespace step first", f)
	}
}

// A namespace that is there again and no longer being deleted is used.
func TestDiagnose_NamespaceActiveAgainAfterTheWait_IsUsed(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}})
	var gets int32
	client.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		if atomic.AddInt32(&gets, 1) == 1 {
			return true, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"},
				Status: corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating}}, nil
		}
		return true, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"},
			Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive}}, nil
	})
	report := DiagnoseCluster(context.Background(), client, "sharko-test", "spoke-1", "N/A")
	if f := failedChecks(report); len(f) != 0 || len(report.SuggestedFixes) != 0 {
		t.Fatalf("a namespace that is back gave failures %q and fixes %+v", f, report.SuggestedFixes)
	}
	passed := 0
	for _, c := range report.NamespaceAccess {
		if c.Passed && strings.HasPrefix(c.Permission, "create secret") {
			passed++
		}
	}
	if passed != 1 {
		t.Fatalf("the secret checks did not run in the namespace that is back: %+v", report.NamespaceAccess)
	}
}
