package diagnose

import (
	"context"
	"time"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
//
// Like Test connection, it makes the test namespace when it is not there yet
// and removes it again afterwards. A namespace that was already there is never
// deleted. A healthy cluster with full rights therefore passes every check
// without anybody making the namespace first.
//
// Each suggested fix is shown once, and only for what really failed. A check
// that cannot run because an earlier one it depends on failed is left out
// instead of failing a second time for the same cause.
func DiagnoseCluster(ctx context.Context, client kubernetes.Interface, namespace, callerARN, roleARN string) *DiagnosticReport {
	report := &DiagnosticReport{
		Identity:       callerARN,
		RoleAssumption: roleARN,
	}

	subject := discoverSubject(ctx, client)
	seen := map[string]bool{}
	record := func(name string, err error) bool {
		check := PermCheck{Permission: name, Passed: err == nil}
		if err != nil {
			check.Error = err.Error()
			fix := generateFix(name, namespace, subject)
			if !seen[fix.YAML] {
				seen[fix.YAML] = true
				report.SuggestedFixes = append(report.SuggestedFixes, fix)
			}
		}
		report.NamespaceAccess = append(report.NamespaceAccess, check)
		return err == nil
	}

	// 1. list namespaces
	_, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	record("list namespaces", err)

	// 2. get namespace. "Not there yet" is fine: it means Sharko may look,
	// and the next step makes it.
	_, err = client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	missing := apierrors.IsNotFound(err)
	if missing {
		err = nil
	}
	record("get namespace "+namespace, err)

	// 3. create the namespace when it is missing.
	createdNamespace := false
	nsUsable := true
	if missing {
		_, err = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   namespace,
				Labels: map[string]string{"app.kubernetes.io/managed-by": "sharko"},
			},
		}, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			err = nil // someone else made it a moment ago; not ours to delete
		} else if err == nil {
			createdNamespace = true
		}
		nsUsable = record("create namespace "+namespace, err)
	}

	// 4. secrets, only when there is a namespace to put them in.
	if nsUsable {
		createOK := record("create secret in "+namespace, func() error {
			_, err := client.CoreV1().Secrets(namespace).Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: testSecretName, Namespace: namespace},
				StringData: map[string]string{"test": "diagnose"},
			}, metav1.CreateOptions{})
			return err
		}())
		if createOK {
			record("get secret in "+namespace, func() error {
				_, err := client.CoreV1().Secrets(namespace).Get(ctx, testSecretName, metav1.GetOptions{})
				return err
			}())
			delErr := client.CoreV1().Secrets(namespace).Delete(ctx, testSecretName, metav1.DeleteOptions{})
			record("delete secret in "+namespace, delErr)
		}
	}

	// 5. leave the cluster as we found it.
	if createdNamespace {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := client.CoreV1().Namespaces().Delete(cctx, namespace, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			record("remove namespace "+namespace, err)
		}
	}

	return report
}

// discoverSubject asks the cluster who Sharko is signed in as, so a suggested
// fix can name the real account. Returns "" when the cluster will not say.
func discoverSubject(ctx context.Context, client kubernetes.Interface) string {
	res, err := client.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil || res == nil {
		return ""
	}
	return res.Status.UserInfo.Username
}
