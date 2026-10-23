package diagnose

import (
	"context"
	"strings"
	"testing"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestDiagnose_AllPass(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}}
	client := fake.NewSimpleClientset(ns)

	report := DiagnoseCluster(context.Background(), client, "sharko-test", "arn:aws:iam::123:role/Test", "N/A")

	if report.Identity != "arn:aws:iam::123:role/Test" {
		t.Errorf("expected identity arn:aws:iam::123:role/Test, got %s", report.Identity)
	}

	if len(report.NamespaceAccess) != 5 {
		t.Fatalf("expected 5 permission checks, got %d", len(report.NamespaceAccess))
	}

	for _, check := range report.NamespaceAccess {
		if !check.Passed {
			t.Errorf("expected check %q to pass, got error: %s", check.Permission, check.Error)
		}
	}

	if len(report.SuggestedFixes) != 0 {
		t.Errorf("expected no suggested fixes, got %d", len(report.SuggestedFixes))
	}
}

func TestDiagnose_SecretCreateForbidden(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}}
	client := fake.NewSimpleClientset(ns)

	client.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "sharko-diagnose-test", nil)
	})

	report := DiagnoseCluster(context.Background(), client, "sharko-test", "arn:aws:iam::123:role/Test", "N/A")

	// First two checks (list ns, get ns) should pass.
	if !report.NamespaceAccess[0].Passed {
		t.Error("expected list namespaces to pass")
	}
	if !report.NamespaceAccess[1].Passed {
		t.Error("expected get namespace to pass")
	}

	// Create secret should fail.
	if report.NamespaceAccess[2].Passed {
		t.Error("expected create secret to fail")
	}
	if report.NamespaceAccess[2].Error == "" {
		t.Error("expected error message for create secret")
	}

	// Should have at least one fix.
	if len(report.SuggestedFixes) == 0 {
		t.Fatal("expected suggested fixes for forbidden create")
	}

	found := false
	for _, fix := range report.SuggestedFixes {
		if strings.Contains(fix.YAML, "sharko-addon-delivery") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected fix YAML to contain sharko-addon-delivery role")
	}
}

func TestDiagnose_NamespaceListForbidden(t *testing.T) {
	client := fake.NewSimpleClientset()

	client.PrependReactor("list", "namespaces", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "", nil)
	})

	report := DiagnoseCluster(context.Background(), client, "sharko-test", "arn:aws:iam::123:role/Test", "N/A")

	if report.NamespaceAccess[0].Passed {
		t.Error("expected list namespaces to fail")
	}

	if len(report.SuggestedFixes) == 0 {
		t.Fatal("expected suggested fixes")
	}

	found := false
	for _, fix := range report.SuggestedFixes {
		if strings.Contains(fix.YAML, "sharko-namespace-list") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected fix YAML to contain sharko-namespace-list")
	}
}

func TestFixGeneration(t *testing.T) {
	tests := []struct {
		name       string
		permission string
		namespace  string
		callerARN  string
		wantInYAML []string
	}{
		{
			name:       "list namespaces fix",
			permission: "list namespaces",
			namespace:  "sharko-test",
			callerARN:  "arn:aws:iam::123:role/Test",
			wantInYAML: []string{"ClusterRole", "sharko-namespace-list", "namespaces", "list"},
		},
		{
			name:       "get namespace fix",
			permission: "get namespace sharko-test",
			namespace:  "sharko-test",
			callerARN:  "arn:aws:iam::123:role/Test",
			wantInYAML: []string{"ClusterRole", "sharko-namespace-get", "namespaces", "get"},
		},
		{
			name:       "create secret fix",
			permission: "create secret in sharko-test",
			namespace:  "sharko-test",
			callerARN:  "arn:aws:iam::123:role/Test",
			wantInYAML: []string{"Role", "sharko-addon-delivery", "sharko-test", "secrets", "create"},
		},
		{
			name:       "delete secret fix",
			permission: "delete secret in sharko-test",
			namespace:  "sharko-test",
			callerARN:  "arn:aws:iam::123:role/Test",
			wantInYAML: []string{"Role", "sharko-addon-delivery", "sharko-test", "secrets", "delete"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fix := generateFix(tt.permission, tt.namespace, "")
			if fix.Description == "" {
				t.Error("expected non-empty description")
			}
			for _, want := range tt.wantInYAML {
				if !strings.Contains(fix.YAML, want) {
					t.Errorf("expected YAML to contain %q, got:\n%s", want, fix.YAML)
				}
			}
		})
	}
}

// Finding 8: a healthy cluster with full rights, where the test namespace does
// not exist yet, must pass every check and leave nothing behind.
func TestDiagnose_HealthyClusterWithoutTestNamespace(t *testing.T) {
	client := fake.NewSimpleClientset()

	report := DiagnoseCluster(context.Background(), client, "sharko-test", "spoke-1", "N/A")

	for _, c := range report.NamespaceAccess {
		if !c.Passed {
			t.Errorf("check %q failed on a healthy cluster: %s", c.Permission, c.Error)
		}
	}
	if len(report.SuggestedFixes) != 0 {
		t.Errorf("expected no fixes, got %d", len(report.SuggestedFixes))
	}
	if _, err := client.CoreV1().Namespaces().Get(context.Background(), "sharko-test", metav1.GetOptions{}); !errors.IsNotFound(err) {
		t.Errorf("the namespace the check made must be removed again, got err=%v", err)
	}
}

// A namespace that was already there is never deleted.
func TestDiagnose_ExistingNamespaceIsKept(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}}
	client := fake.NewSimpleClientset(ns)

	DiagnoseCluster(context.Background(), client, "sharko-test", "spoke-1", "N/A")

	if _, err := client.CoreV1().Namespaces().Get(context.Background(), "sharko-test", metav1.GetOptions{}); err != nil {
		t.Errorf("an existing namespace must stay, got err=%v", err)
	}
}

// Missing namespace and no right to create it: one failure, one fix, and the
// secret checks are not run (they would only fail for the same cause).
func TestDiagnose_CannotCreateNamespace_OneFailureOneFix(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "sharko-test", nil)
	})

	report := DiagnoseCluster(context.Background(), client, "sharko-test", "spoke-1", "N/A")

	failed := 0
	for _, c := range report.NamespaceAccess {
		if !c.Passed {
			failed++
		}
		if strings.Contains(c.Permission, "secret") {
			t.Errorf("secret checks must not run without a namespace, saw %q", c.Permission)
		}
	}
	if failed != 1 {
		t.Errorf("want exactly 1 failed check, got %d: %+v", failed, report.NamespaceAccess)
	}
	if len(report.SuggestedFixes) != 1 {
		t.Fatalf("want exactly 1 fix, got %d", len(report.SuggestedFixes))
	}
	if !strings.Contains(report.SuggestedFixes[0].YAML, "sharko-namespace-manage") {
		t.Errorf("fix should be about creating the namespace:\n%s", report.SuggestedFixes[0].YAML)
	}
}

// Finding 8: the same block used to show three times, once per secret check.
func TestDiagnose_SecretFixShownOnce(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}}
	client := fake.NewSimpleClientset(ns)
	for _, verb := range []string{"get", "delete"} {
		client.PrependReactor(verb, "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "x", nil)
		})
	}
	report := DiagnoseCluster(context.Background(), client, "sharko-test", "spoke-1", "N/A")
	if len(report.SuggestedFixes) != 1 {
		t.Errorf("want 1 fix, got %d", len(report.SuggestedFixes))
	}
}

// Only the fixes for what failed: a forbidden secret create must not bring a
// namespace fix along.
func TestDiagnose_OnlyFixesForWhatFailed(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sharko-test"}}
	client := fake.NewSimpleClientset(ns)
	client.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "x", nil)
	})
	report := DiagnoseCluster(context.Background(), client, "sharko-test", "spoke-1", "N/A")
	if len(report.SuggestedFixes) != 1 || !strings.Contains(report.SuggestedFixes[0].YAML, "sharko-addon-delivery") {
		t.Fatalf("want only the secret fix, got %+v", report.SuggestedFixes)
	}
}

// No made-up "sharko-access" group. With an unknown account the fix carries a
// plain placeholder; with a known one it names that account.
func TestFix_NoMadeUpGroup(t *testing.T) {
	for _, perm := range []string{"list namespaces", "get namespace x", "create namespace x", "create secret in x"} {
		unknown := generateFix(perm, "x", "")
		if strings.Contains(unknown.YAML, "sharko-access") {
			t.Errorf("%s: made-up group is back:\n%s", perm, unknown.YAML)
		}
		if !strings.Contains(unknown.YAML, unknownSubjectName) {
			t.Errorf("%s: placeholder missing:\n%s", perm, unknown.YAML)
		}
		known := generateFix(perm, "x", "system:serviceaccount:sharko:sharko")
		if !strings.Contains(known.YAML, "name: system:serviceaccount:sharko:sharko") || strings.Contains(known.YAML, unknownSubjectName) {
			t.Errorf("%s: real account not used:\n%s", perm, known.YAML)
		}
	}
}

// The account the cluster reports is used in the fix.
func TestDiagnose_FixNamesTheRealAccount(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authv1.SelfSubjectReview{Status: authv1.SelfSubjectReviewStatus{UserInfo: authv1.UserInfo{Username: "sharko-bot"}}}, nil
	})
	client.PrependReactor("list", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "", nil)
	})
	report := DiagnoseCluster(context.Background(), client, "sharko-test", "spoke-1", "N/A")
	if len(report.SuggestedFixes) == 0 || !strings.Contains(report.SuggestedFixes[0].YAML, "name: sharko-bot") {
		t.Fatalf("fix should name sharko-bot: %+v", report.SuggestedFixes)
	}
}
