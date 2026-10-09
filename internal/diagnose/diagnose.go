package diagnose

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DiagnosticReport contains the results of a cluster permission diagnostic.
type DiagnosticReport struct {
	Identity        string      `json:"identity"`
	RoleAssumption  string      `json:"role_assumption"`
	NamespaceAccess []PermCheck `json:"namespace_access"`
	SuggestedFixes  []Fix       `json:"suggested_fixes"`
}

// PermCheck records whether a specific permission check passed or failed.
type PermCheck struct {
	Permission string `json:"permission"`
	Passed     bool   `json:"passed"`
	Error      string `json:"error,omitempty"`
}

// Fix contains a suggested remediation with copy-paste-ready YAML.
type Fix struct {
	Description string `json:"description"`
	YAML        string `json:"yaml"`
}

const testSecretName = "sharko-diagnose-test"

// DiagnoseCluster runs a series of permission checks against a remote cluster
// and returns a report with pass/fail results and suggested fixes for failures.
func DiagnoseCluster(ctx context.Context, client kubernetes.Interface, namespace, callerARN, roleARN string) *DiagnosticReport {
	report := &DiagnosticReport{
		Identity:       callerARN,
		RoleAssumption: roleARN,
	}

	checks := []struct {
		Name string
		Fn   func() error
	}{
		{"list namespaces", func() error {
			_, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
			return err
		}},
		{"get namespace " + namespace, func() error {
			_, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
			return err
		}},
		{"create secret in " + namespace, func() error {
			_, err := client.CoreV1().Secrets(namespace).Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: testSecretName, Namespace: namespace},
				StringData: map[string]string{"test": "diagnose"},
			}, metav1.CreateOptions{})
			return err
		}},
		{"get secret in " + namespace, func() error {
			_, err := client.CoreV1().Secrets(namespace).Get(ctx, testSecretName, metav1.GetOptions{})
			return err
		}},
		{"delete secret in " + namespace, func() error {
			return client.CoreV1().Secrets(namespace).Delete(ctx, testSecretName, metav1.DeleteOptions{})
		}},
	}

	createdSecret := false
	for _, c := range checks {
		err := c.Fn()
		check := PermCheck{Permission: c.Name, Passed: err == nil}
		if err != nil {
			check.Error = err.Error()
			report.SuggestedFixes = append(report.SuggestedFixes, generateFix(c.Name, namespace, callerARN))
		}
		if c.Name == "create secret in "+namespace && err == nil {
			createdSecret = true
		}
		report.NamespaceAccess = append(report.NamespaceAccess, check)
	}

	// Best-effort cleanup if we created but delete might have failed or not run.
	if createdSecret {
		_ = client.CoreV1().Secrets(namespace).Delete(ctx, testSecretName, metav1.DeleteOptions{})
	}

	return report
}
