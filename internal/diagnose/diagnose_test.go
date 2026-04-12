package diagnose

import (
	"context"
	"strings"
	"testing"

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
			fix := generateFix(tt.permission, tt.namespace, tt.callerARN)
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
