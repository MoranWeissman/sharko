package verify

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Stage1 verifies connectivity to a Kubernetes cluster by performing a full
// secret CRUD cycle: ensure namespace -> create secret -> read back -> delete.
func Stage1(ctx context.Context, client kubernetes.Interface, namespace string) Result {
	start := time.Now()

	// 1. Get server version (informational).
	var serverVersion string
	if version, err := client.Discovery().ServerVersion(); err == nil {
		serverVersion = version.GitVersion
	}

	// 2. Ensure namespace exists (create if absent, never delete).
	_, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		}, metav1.CreateOptions{})
		if err != nil {
			return failResult("stage1", err, time.Since(start), serverVersion)
		}
	} else if err != nil {
		return failResult("stage1", err, time.Since(start), serverVersion)
	}

	// 3. Create test secret.
	secretName := fmt.Sprintf("sharko-connectivity-test-%d", time.Now().Unix())
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "sharko",
			},
		},
		StringData: map[string]string{"test": "sharko-verify"},
	}
	_, err = client.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		return failResult("stage1", err, time.Since(start), serverVersion)
	}

	// 4. Read back.
	_, err = client.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return failResult("stage1", err, time.Since(start), serverVersion)
	}

	// 5. Delete.
	err = client.CoreV1().Secrets(namespace).Delete(ctx, secretName, metav1.DeleteOptions{})
	if err != nil {
		return failResult("stage1", err, time.Since(start), serverVersion)
	}

	return Result{
		Success:       true,
		Stage:         "stage1",
		DurationMs:    time.Since(start).Milliseconds(),
		ServerVersion: serverVersion,
	}
}

// failResult builds a failed Result with classified error code.
func failResult(stage string, err error, duration time.Duration, serverVersion string) Result {
	return Result{
		Success:       false,
		Stage:         stage,
		ErrorCode:     ClassifyError(err),
		ErrorMessage:  err.Error(),
		DurationMs:    duration.Milliseconds(),
		ServerVersion: serverVersion,
	}
}
