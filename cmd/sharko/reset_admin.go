package main

import (
	"context"
	cryptoRand "crypto/rand"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// initialAdminSecretName is the canonical name of the dedicated bootstrap
// admin secret. Mirrors `internal/auth.InitialAdminSecretName`. We duplicate
// the literal here (rather than importing internal/auth) to keep the
// reset-admin CLI free of the auth package's transitive dependencies — the
// same trade-off V124-6.3 made for resetGeneratePassword / resetDetectNamespace.
const initialAdminSecretName = "sharko-initial-admin-secret"

// envWriteInitialAdminSecret mirrors auth.EnvWriteInitialAdminSecret. We
// duplicate the literal for the same reason as initialAdminSecretName.
const envWriteInitialAdminSecret = "SHARKO_WRITE_INITIAL_ADMIN_SECRET"

var resetAdminCmd = &cobra.Command{
	Use:   "reset-admin",
	Short: "Reset the admin password (requires kubectl access)",
	Long: `Reset the admin user's password by directly updating the Kubernetes Secret.

This command connects to the Kubernetes cluster using in-cluster config or
the kubeconfig file specified by --kubeconfig / KUBECONFIG env var.
It generates a new 32-character random password, updates the bcrypt hash
in the Sharko Secret, prints the new password to stdout, and rotates the
dedicated 'sharko-initial-admin-secret' to carry the new plaintext (so
operators can retrieve it via kubectl after the rotation, mirroring the
ArgoCD argocd-initial-admin-secret pattern).

Set SHARKO_WRITE_INITIAL_ADMIN_SECRET=false (or Helm value
bootstrapAdmin.writeInitialSecret=false) to opt out of the dedicated
secret — in that mode reset-admin only deletes any stale secret and does
NOT recreate it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		kubeconfigPath, _ := cmd.Flags().GetString("kubeconfig")
		namespace, _ := cmd.Flags().GetString("namespace")
		secretName, _ := cmd.Flags().GetString("secret")

		clientset, err := buildK8sClient(kubeconfigPath)
		if err != nil {
			return fmt.Errorf("failed to create Kubernetes client: %w", err)
		}

		ctx := context.Background()
		result, err := runResetAdmin(ctx, clientset, namespace, secretName)
		if err != nil {
			return err
		}

		if result.RewroteInitialAdminSecret {
			fmt.Printf("Rotated %s/%s to carry the new plaintext.\n", namespace, initialAdminSecretName)
		} else if result.DeletedStaleInitialAdminSecret {
			fmt.Printf("Deleted stale %s/%s (writeInitialSecret=false; not recreated).\n", namespace, initialAdminSecretName)
		}

		fmt.Printf("Admin password has been reset.\n")
		fmt.Printf("New password: %s\n", result.NewPassword)
		fmt.Println()
		fmt.Println("WARNING: This password will not be shown again. Store it securely.")
		if result.RewroteInitialAdminSecret {
			fmt.Printf("It is also available via:\n")
			fmt.Printf("  kubectl get secret %s -n %s -o jsonpath='{.data.password}' | base64 -d\n",
				initialAdminSecretName, namespace)
		}

		return nil
	},
}

// resetAdminResult carries the outcome of runResetAdmin so callers (cobra
// RunE in production, tests in cmd/sharko) can render output / make
// assertions without re-deriving state from the cluster.
type resetAdminResult struct {
	NewPassword                    string
	RewroteInitialAdminSecret      bool
	DeletedStaleInitialAdminSecret bool
}

// runResetAdmin is the testable core of `sharko reset-admin`. It updates the
// admin password in the Sharko Secret (bcrypt hash) and rotates (or deletes,
// per opt-out) the dedicated `sharko-initial-admin-secret` so kubectl-based
// retrieval of the bootstrap password persists across rotations.
//
// V124-7.1 / BUG-025: previously this only deleted the dedicated secret on
// rotation, which forced operators back to log-grep for every subsequent
// rotation. We now rotate (delete-then-rewrite) so the secret continues to
// reflect the CURRENT initial admin plaintext.
//
// Replace, not patch: we explicitly delete the old secret before writing a
// new one (rather than patching `data.password`) so no stale fields, labels,
// or annotations from a previous version of Sharko survive the rotation.
func runResetAdmin(ctx context.Context, clientset kubernetes.Interface, namespace, secretName string) (resetAdminResult, error) {
	var result resetAdminResult

	// Read the existing Sharko Secret so we don't blow away other keys
	// (admin.password is one of many — bootstrap markers, github_token, etc.).
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return result, fmt.Errorf("failed to read Secret %s/%s: %w", namespace, secretName, err)
	}

	// Generate a new 32-char password and bcrypt-hash it.
	password := resetGeneratePassword(32)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return result, fmt.Errorf("failed to hash password: %w", err)
	}

	// Update the Sharko Secret: bcrypt hash on admin.password, drop the
	// initialPassword marker (already rotated, so the marker is stale).
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data["admin.password"] = hash
	delete(secret.Data, "admin.initialPassword")

	if _, err := clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return result, fmt.Errorf("failed to update Secret: %w", err)
	}

	// Rotate (or delete-only, per opt-out) the dedicated initial-admin-secret.
	if resetWriteInitialAdminSecretEnabled() {
		// V124-7.1: rotate via delete-then-rewrite. This guarantees no stale
		// fields (labels, annotations, extra data keys from a previous
		// Sharko version) survive across the rotation — replace, not patch.
		if err := deleteInitialAdminSecret(ctx, clientset, namespace); err != nil {
			return result, fmt.Errorf("rotate initial-admin-secret (delete step): %w", err)
		}
		if err := writeInitialAdminSecretCLI(ctx, clientset, namespace, password); err != nil {
			return result, fmt.Errorf("rotate initial-admin-secret (write step): %w", err)
		}
		result.RewroteInitialAdminSecret = true
	} else {
		// Opt-out path: operator disabled the dedicated secret via
		// `bootstrapAdmin.writeInitialSecret: false` (Helm). Preserve V124-6.3
		// cleanup behavior — delete any stale secret but DO NOT recreate.
		// Idempotent if the secret was never created.
		err := clientset.CoreV1().Secrets(namespace).Delete(ctx, initialAdminSecretName, metav1.DeleteOptions{})
		switch {
		case err == nil:
			result.DeletedStaleInitialAdminSecret = true
		case errors.IsNotFound(err):
			// no-op: nothing to delete
		default:
			// Non-fatal: the password reset already succeeded. Surface the
			// error to the caller so the CLI can warn but not fail.
			fmt.Fprintf(os.Stderr, "warning: failed to delete %s/%s after reset: %v\n",
				namespace, initialAdminSecretName, err)
		}
	}

	result.NewPassword = password
	return result, nil
}

// deleteInitialAdminSecret removes the dedicated bootstrap admin secret.
// Idempotent on missing — V124-7 invariant: reset-admin always rotates,
// so callers can rely on this returning nil whether or not the secret
// previously existed.
func deleteInitialAdminSecret(ctx context.Context, clientset kubernetes.Interface, namespace string) error {
	err := clientset.CoreV1().Secrets(namespace).Delete(ctx, initialAdminSecretName, metav1.DeleteOptions{})
	if err == nil || errors.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("delete %s/%s: %w", namespace, initialAdminSecretName, err)
}

// writeInitialAdminSecretCLI creates `sharko-initial-admin-secret` with the
// given plaintext password. Mirrors `internal/auth.Store.writeInitialAdminSecret`
// (kept in sync deliberately — see initialAdminSecretName comment for why we
// duplicate rather than import).
//
// The annotation `sharko.io/initial-secret: "rotated-on-reset-admin"` reflects
// V124-7 behavior: the secret persists across `sharko reset-admin` rotations,
// each rotation rewriting `data.password` to the new plaintext. Operators who
// no longer need it can `kubectl delete` it manually at any time.
//
// This helper assumes the previous version (if any) has already been deleted
// — callers in V124-7 do delete-then-write to enforce replace-not-patch
// semantics. If a Create returns AlreadyExists (e.g. a racing reconciler
// recreated it), we fall back to Update so reset-admin remains idempotent
// against transient races.
func writeInitialAdminSecretCLI(ctx context.Context, clientset kubernetes.Interface, namespace, password string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      initialAdminSecretName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "sharko",
				"app.kubernetes.io/component":  "bootstrap",
			},
			Annotations: map[string]string{
				"sharko.io/initial-secret": "rotated-on-reset-admin",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte(password),
		},
	}

	if _, err := clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("create %s/%s: %w", namespace, initialAdminSecretName, err)
		}
		// Race fallback: someone (or the auth-store reconciler) recreated the
		// secret between our Delete and our Create. Update it instead so we
		// still end up with the new plaintext + V124-7 annotation/labels.
		existing, getErr := clientset.CoreV1().Secrets(namespace).Get(ctx, initialAdminSecretName, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("get existing %s/%s for update: %w", namespace, initialAdminSecretName, getErr)
		}
		existing.Labels = secret.Labels
		existing.Annotations = secret.Annotations
		existing.Data = secret.Data
		existing.Type = secret.Type
		if _, err := clientset.CoreV1().Secrets(namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update %s/%s: %w", namespace, initialAdminSecretName, err)
		}
	}
	return nil
}

// resetWriteInitialAdminSecretEnabled mirrors
// `internal/auth.writeInitialAdminSecretEnabled`. Default true; "false"/"0"/"no"
// (case-insensitive) opts out. Helm value `bootstrapAdmin.writeInitialSecret`
// drives this via the SHARKO_WRITE_INITIAL_ADMIN_SECRET env var on the pod.
func resetWriteInitialAdminSecretEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(envWriteInitialAdminSecret)))
	if v == "false" || v == "0" || v == "no" {
		return false
	}
	return true
}

func buildK8sClient(kubeconfigPath string) (kubernetes.Interface, error) {
	// Try in-cluster config first
	config, err := rest.InClusterConfig()
	if err == nil {
		return kubernetes.NewForConfig(config)
	}

	// Fall back to kubeconfig
	if kubeconfigPath == "" {
		kubeconfigPath = os.Getenv("KUBECONFIG")
	}
	if kubeconfigPath == "" {
		home, _ := os.UserHomeDir()
		kubeconfigPath = home + "/.kube/config"
	}

	config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from %s: %w", kubeconfigPath, err)
	}

	return kubernetes.NewForConfig(config)
}

// resetGeneratePassword generates a random password of the given length.
// This is a standalone copy to avoid importing internal/auth from cmd/.
func resetGeneratePassword(length int) string {
	const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, length)
	randBytes := make([]byte, length)
	cryptoRand.Read(randBytes)
	for i := range b {
		b[i] = chars[int(randBytes[i])%len(chars)]
	}
	return string(b)
}

func resetDetectNamespace() string {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data))
	}
	if ns := os.Getenv("SHARKO_NAMESPACE"); ns != "" {
		return ns
	}
	return "sharko"
}

func init() {
	resetAdminCmd.Flags().String("kubeconfig", "", "Path to kubeconfig file (defaults to KUBECONFIG env or ~/.kube/config)")
	resetAdminCmd.Flags().String("namespace", resetDetectNamespace(), "Kubernetes namespace where Sharko is installed")
	resetAdminCmd.Flags().String("secret", "sharko", "Name of the Sharko Secret")

	rootCmd.AddCommand(resetAdminCmd)
}
