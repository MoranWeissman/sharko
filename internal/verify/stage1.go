package verify

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// Every sentence Stage 1 hands back — Result.ErrorMessage and every failed
// Step.Detail — is a catalog sentence chosen by ErrorCode. Not one character
// of what the cluster said reaches a person.
//
// # The shape
//
//	classify at the boundary   ClassifyError(err) -> a stable ErrorCode
//	choose a safe sentence     FriendlyMessage(code) -> SafeMessage
//	keep the cause for the log Result.diagnostic (unexported, so unmarshallable)
//
// # What was here before, and how bad it actually was
//
// failResult set ErrorMessage to err.Error(), all six Step.Detail values were
// err.Error(), and FriendlyMessage glued a hint onto that raw text. The trace
// says a credentials-MARKED error cannot reach these lines today: the markers
// go on at the credential fetch (internal/providers) and the client build
// (remoteclient.NewClientFromKubeconfig), and both of those return on branches
// that never call Stage 1. Every error that does arrive comes from the
// kubernetes.Interface below.
//
// So what actually leaked was the destination cluster's own words — API server
// hostnames and resolved IPs from *url.Error, internal DNS resolvers, the
// ServiceAccount identity out of a 403, certificate names from x509 failures,
// and whatever text a third-party admission webhook felt like returning. Real,
// but topology detail rather than credential material.
//
// It is fixed unconditionally anyway. The rule is that no raw provider,
// credential-store, Git, Kubernetes or internal error text is part of a
// user-facing message, and "today's trace says it cannot reach here" is not
// one of the exceptions — Stage 1 is handed a client by its caller and cannot
// see where that client came from.

// Stage1 verifies connectivity to a Kubernetes cluster by performing a full
// secret CRUD cycle: ensure namespace -> create secret -> read back -> delete.
func Stage1(ctx context.Context, client kubernetes.Interface, namespace string) Result {
	start := time.Now()
	var steps []Step

	// 1. Get server version (informational).
	var serverVersion string
	if version, err := client.Discovery().ServerVersion(); err != nil {
		steps = append(steps, Step{Name: "Fetch server version", Status: "fail", Detail: safeDetail(err)})
		return failResult("stage1", err, time.Since(start), serverVersion, steps)
	} else {
		serverVersion = version.GitVersion
		steps = append(steps, Step{Name: "Fetch server version", Status: "pass", Detail: serverVersion})
	}

	// 2. Ensure namespace exists (create if absent, never delete).
	_, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		}, metav1.CreateOptions{})
		if err != nil {
			steps = append(steps, Step{Name: "Ensure namespace", Status: "fail", Detail: safeDetail(err)})
			return failResultSkipping("stage1", err, time.Since(start), serverVersion, steps,
				"Create test secret", "Read back test secret", "Delete test secret")
		}
		steps = append(steps, Step{Name: "Ensure namespace", Status: "pass", Detail: "created"})
	} else if err != nil {
		steps = append(steps, Step{Name: "Ensure namespace", Status: "fail", Detail: safeDetail(err)})
		return failResultSkipping("stage1", err, time.Since(start), serverVersion, steps,
			"Create test secret", "Read back test secret", "Delete test secret")
	} else {
		steps = append(steps, Step{Name: "Ensure namespace", Status: "pass", Detail: "already exists"})
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
		steps = append(steps, Step{Name: "Create test secret", Status: "fail", Detail: safeDetail(err)})
		return failResultSkipping("stage1", err, time.Since(start), serverVersion, steps,
			"Read back test secret", "Delete test secret")
	}
	steps = append(steps, Step{Name: "Create test secret", Status: "pass"})

	// 4. Read back.
	_, err = client.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		steps = append(steps, Step{Name: "Read back test secret", Status: "fail", Detail: safeDetail(err)})
		return failResultSkipping("stage1", err, time.Since(start), serverVersion, steps,
			"Delete test secret")
	}
	steps = append(steps, Step{Name: "Read back test secret", Status: "pass"})

	// 5. Delete.
	err = client.CoreV1().Secrets(namespace).Delete(ctx, secretName, metav1.DeleteOptions{})
	if err != nil {
		steps = append(steps, Step{Name: "Delete test secret", Status: "fail", Detail: safeDetail(err)})
		return failResult("stage1", err, time.Since(start), serverVersion, steps)
	}
	steps = append(steps, Step{Name: "Delete test secret", Status: "pass"})

	return Result{
		Success:       true,
		Stage:         "stage1",
		DurationMs:    time.Since(start).Milliseconds(),
		ServerVersion: serverVersion,
		Steps:         steps,
	}
}

// safeDetail is the sentence a failed step shows a person.
//
// Step.Detail is serialized on every cluster-test response (twice — as `steps`
// and again inside `result`), so it is exactly as public as ErrorMessage and
// gets exactly the same treatment: classify at the boundary, then say the
// catalog sentence for the code. Nothing the cluster said reaches it.
func safeDetail(err error) string {
	return FriendlyMessage(ClassifyError(err)).String()
}

// failResult builds a failed Result: a stable code, the catalog sentence for
// that code, and the real cause tucked into the unexported diagnostic field
// for the server-side log.
func failResult(stage string, err error, duration time.Duration, serverVersion string, steps []Step) Result {
	code := ClassifyError(err)
	return Result{
		Success:       false,
		Stage:         stage,
		ErrorCode:     code,
		ErrorMessage:  FriendlyMessage(code).String(),
		DurationMs:    duration.Milliseconds(),
		ServerVersion: serverVersion,
		Steps:         steps,
		// credsafe.Sentence, not err.Error(): a credentials-backend error
		// says the fixed safe sentence even here, where only the server log
		// can see it.
		diagnostic: credsafe.Sentence(err),
	}
}

// failResultSkipping builds a failed Result and appends skipped steps for remaining work.
func failResultSkipping(stage string, err error, duration time.Duration, serverVersion string, steps []Step, skipped ...string) Result {
	for _, name := range skipped {
		steps = append(steps, Step{Name: name, Status: "skipped"})
	}
	return failResult(stage, err, duration, serverVersion, steps)
}
