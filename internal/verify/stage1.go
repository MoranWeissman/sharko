package verify

import (
	"context"
	"fmt"
	"log/slog"
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

// PreviewNotCheckedSentence is what a preview (dry run) of register or adopt
// says about the part of the connection test it skipped. A preview only
// reads from the cluster, so it cannot know whether Sharko is allowed to
// write there. The sentence is pinned by exact text in stage1_test.go.
const PreviewNotCheckedSentence = "Write access was not checked because this is a preview."

// previewNamespaceMissingDetail is the "Check namespace" detail on a preview
// when the test namespace is not on the cluster yet.
const previewNamespaceMissingDetail = "not there yet; the real check creates it and removes it afterwards"

// namespaceCreatedAndRemovedDetail replaces "created" on the "Ensure
// namespace" step once Sharko has removed the namespace it made for this run.
const namespaceCreatedAndRemovedDetail = "created for this test, then removed"

// How long Stage 1 waits for a test namespace that is still being deleted
// (for example by the cleanup of a test that ran a few seconds earlier)
// before it creates the namespace again. A namespace that is being deleted
// refuses new Secrets, so without this wait a Test connection followed
// quickly by a register would fail for no real reason.
const (
	terminatingPollInterval = 500 * time.Millisecond
	terminatingWaitLimit    = 30 * time.Second
)

// The three write steps. Both forms of the check list them under the same
// names, so the step list a person sees has the same shape either way.
const (
	stepCreateSecret = "Create test secret"
	stepReadSecret   = "Read back test secret"
	stepDeleteSecret = "Delete test secret"
)

// Stage1 verifies connectivity to a Kubernetes cluster by performing a full
// secret CRUD cycle: ensure namespace -> create secret -> read back -> delete.
//
// It is for explicit actions only: Test connection, the connection doctor,
// and a REAL (not dry-run) register or adopt. A preview must call
// Stage1Preview, which writes nothing.
//
// Stage 1 cleans up after itself. The test Secret is removed on every path
// where it was created, and the test namespace is removed when THIS run
// created it. A namespace that was already there is never deleted: somebody
// else made it, and it may hold things that are not Sharko's. A cleanup that
// fails never turns a passing check into a failing one; it adds a sentence
// to Result.Warnings and writes a log line.
func Stage1(ctx context.Context, client kubernetes.Interface, namespace string) Result {
	start := time.Now()
	var steps []Step

	// What this run made, so the cleanup below removes exactly that and
	// nothing else.
	createdNamespace := false
	secretName := ""
	secretLeft := false

	finish := func(r Result) Result {
		r.Warnings = append(r.Warnings, cleanupStage1(ctx, client, namespace, secretName, secretLeft, createdNamespace, r.Steps)...)
		return r
	}

	// 1. Get server version (informational).
	var serverVersion string
	if version, err := client.Discovery().ServerVersion(); err != nil {
		steps = append(steps, Step{Name: "Fetch server version", Status: "fail", Detail: safeDetail(err)})
		return failResult("stage1", err, time.Since(start), serverVersion, steps)
	} else {
		serverVersion = version.GitVersion
		steps = append(steps, Step{Name: "Fetch server version", Status: "pass", Detail: serverVersion})
	}

	// 2. Ensure namespace exists (create if absent).
	existing, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err == nil && isTerminating(existing) && waitForNamespaceGone(ctx, client, namespace) {
		err = apierrors.NewNotFound(corev1.Resource("namespaces"), namespace)
	}
	if apierrors.IsNotFound(err) {
		_, err = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "sharko",
				},
			},
		}, metav1.CreateOptions{})
		switch {
		case apierrors.IsAlreadyExists(err):
			// Someone else made it between our get and our create, for
			// example a second check running at the same time. It is there,
			// so the check goes on; it is not ours, so the cleanup never
			// deletes it (createdNamespace stays false).
			steps = append(steps, Step{Name: "Ensure namespace", Status: "pass", Detail: "already exists"})
		case err != nil:
			steps = append(steps, Step{Name: "Ensure namespace", Status: "fail", Detail: safeDetail(err)})
			return failResultSkipping("stage1", err, time.Since(start), serverVersion, steps,
				stepCreateSecret, stepReadSecret, stepDeleteSecret)
		default:
			createdNamespace = true
			steps = append(steps, Step{Name: "Ensure namespace", Status: "pass", Detail: "created"})
		}
	} else if err != nil {
		steps = append(steps, Step{Name: "Ensure namespace", Status: "fail", Detail: safeDetail(err)})
		return failResultSkipping("stage1", err, time.Since(start), serverVersion, steps,
			stepCreateSecret, stepReadSecret, stepDeleteSecret)
	} else {
		steps = append(steps, Step{Name: "Ensure namespace", Status: "pass", Detail: "already exists"})
	}

	// 3. Create test secret.
	name := fmt.Sprintf("sharko-connectivity-test-%d", time.Now().Unix())
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "sharko",
			},
		},
		StringData: map[string]string{"test": "sharko-verify"},
	}
	_, err = client.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		steps = append(steps, Step{Name: stepCreateSecret, Status: "fail", Detail: safeDetail(err)})
		return finish(failResultSkipping("stage1", err, time.Since(start), serverVersion, steps,
			stepReadSecret, stepDeleteSecret))
	}
	secretName, secretLeft = name, true
	steps = append(steps, Step{Name: stepCreateSecret, Status: "pass"})

	// 4. Read back.
	_, err = client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		steps = append(steps, Step{Name: stepReadSecret, Status: "fail", Detail: safeDetail(err)})
		return finish(failResultSkipping("stage1", err, time.Since(start), serverVersion, steps,
			stepDeleteSecret))
	}
	steps = append(steps, Step{Name: stepReadSecret, Status: "pass"})

	// 5. Delete. This is part of the check (it proves Sharko may delete),
	// so a failure here fails the test. The cleanup below still tries once
	// more and says so if the Secret stays behind.
	err = client.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		steps = append(steps, Step{Name: stepDeleteSecret, Status: "fail", Detail: safeDetail(err)})
		return finish(failResult("stage1", err, time.Since(start), serverVersion, steps))
	}
	secretLeft = false
	steps = append(steps, Step{Name: stepDeleteSecret, Status: "pass"})

	return finish(Result{
		Success:       true,
		Stage:         "stage1",
		DurationMs:    time.Since(start).Milliseconds(),
		ServerVersion: serverVersion,
		Steps:         steps,
	})
}

// Stage1Preview is the read-only form of Stage1, for a preview (dry run) of
// register or adopt. It reads the server version and looks the test
// namespace up, so the preview can still say whether the cluster answers.
// It makes NO write: no namespace create, no Secret. The three write steps
// come back "skipped", and Result.Notes says in plain words that write
// access was not checked.
func Stage1Preview(ctx context.Context, client kubernetes.Interface, namespace string) Result {
	start := time.Now()
	var steps []Step
	notChecked := []string{PreviewNotCheckedSentence}
	skippedWrites := []Step{
		{Name: stepCreateSecret, Status: "skipped", Detail: PreviewNotCheckedSentence},
		{Name: stepReadSecret, Status: "skipped", Detail: PreviewNotCheckedSentence},
		{Name: stepDeleteSecret, Status: "skipped", Detail: PreviewNotCheckedSentence},
	}

	var serverVersion string
	version, err := client.Discovery().ServerVersion()
	if err != nil {
		steps = append(steps, Step{Name: "Fetch server version", Status: "fail", Detail: safeDetail(err)})
		steps = append(steps, Step{Name: "Check namespace", Status: "skipped"})
		r := failResult("stage1", err, time.Since(start), serverVersion, append(steps, skippedWrites...))
		r.Notes = notChecked
		return r
	}
	serverVersion = version.GitVersion
	steps = append(steps, Step{Name: "Fetch server version", Status: "pass", Detail: serverVersion})

	existing, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err), err == nil && isTerminating(existing):
		steps = append(steps, Step{Name: "Check namespace", Status: "pass", Detail: previewNamespaceMissingDetail})
	case err != nil:
		steps = append(steps, Step{Name: "Check namespace", Status: "fail", Detail: safeDetail(err)})
		r := failResult("stage1", err, time.Since(start), serverVersion, append(steps, skippedWrites...))
		r.Notes = notChecked
		return r
	default:
		steps = append(steps, Step{Name: "Check namespace", Status: "pass", Detail: "already exists"})
	}

	return Result{
		Success:       true,
		Stage:         "stage1",
		DurationMs:    time.Since(start).Milliseconds(),
		ServerVersion: serverVersion,
		Steps:         append(steps, skippedWrites...),
		Notes:         notChecked,
	}
}

// cleanupStage1 removes what one Stage1 run made and returns a warning
// sentence for each thing it could not remove. It runs with its own short
// context so a caller's deadline that already passed does not stop it from
// trying.
func cleanupStage1(ctx context.Context, client kubernetes.Interface, namespace, secretName string, secretLeft, createdNamespace bool, steps []Step) []string {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	var warnings []string
	if secretLeft && secretName != "" {
		err := client.CoreV1().Secrets(namespace).Delete(cctx, secretName, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			logCleanupFailure("secret", namespace, secretName, err)
			// Only worth saying when the namespace stays: removing a
			// namespace Sharko made takes the Secret with it.
			if !createdNamespace {
				warnings = append(warnings, fmt.Sprintf(
					"Sharko could not remove its test Secret %q from namespace %q. It holds no real data, and you can delete it by hand.",
					secretName, namespace))
			}
		}
	}
	if createdNamespace {
		err := client.CoreV1().Namespaces().Delete(cctx, namespace, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			logCleanupFailure("namespace", namespace, namespace, err)
			warnings = append(warnings, fmt.Sprintf(
				"Sharko created the namespace %q for this test but could not remove it afterwards. You can delete it by hand.",
				namespace))
		} else {
			for i := range steps {
				if steps[i].Name == "Ensure namespace" && steps[i].Detail == "created" {
					steps[i].Detail = namespaceCreatedAndRemovedDetail
				}
			}
		}
	}
	return warnings
}

// logCleanupFailure is the server-side log line for a cleanup that did not
// work. Like every Stage 1 log, it carries the classified code and the
// credsafe sentence, never the raw response body of the cluster.
func logCleanupFailure(kind, namespace, name string, err error) {
	slog.Warn("connection test cleanup failed; Sharko left a test object on the cluster",
		"kind", kind, "namespace", namespace, "name", name,
		"code", ClassifyError(err), "diagnostic", credsafe.Sentence(err))
}

func isTerminating(ns *corev1.Namespace) bool {
	return ns != nil && ns.Status.Phase == corev1.NamespaceTerminating
}

// waitForNamespaceGone waits until a namespace that is being deleted is
// really gone. It returns true when it is gone, false when it is still there
// after the wait limit (or the context ended), in which case Stage 1 carries
// on and the next write fails with a normal, classified error.
func waitForNamespaceGone(ctx context.Context, client kubernetes.Interface, namespace string) bool {
	deadline := time.Now().Add(terminatingWaitLimit)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(terminatingPollInterval):
		}
		ns, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true
		}
		if err != nil || !isTerminating(ns) {
			return false
		}
	}
	return false
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
