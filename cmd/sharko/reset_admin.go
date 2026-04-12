package main

import (
	"context"
	cryptoRand "crypto/rand"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var resetAdminCmd = &cobra.Command{
	Use:   "reset-admin",
	Short: "Reset the admin password (requires kubectl access)",
	Long: `Reset the admin user's password by directly updating the Kubernetes Secret.

This command connects to the Kubernetes cluster using in-cluster config or
the kubeconfig file specified by --kubeconfig / KUBECONFIG env var.
It generates a new 32-character random password, updates the bcrypt hash
in the Sharko Secret, and prints the new password to stdout.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		kubeconfigPath, _ := cmd.Flags().GetString("kubeconfig")
		namespace, _ := cmd.Flags().GetString("namespace")
		secretName, _ := cmd.Flags().GetString("secret")

		clientset, err := buildK8sClient(kubeconfigPath)
		if err != nil {
			return fmt.Errorf("failed to create Kubernetes client: %w", err)
		}

		ctx := context.Background()

		// Read the existing Secret
		secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to read Secret %s/%s: %w", namespace, secretName, err)
		}

		// Generate a new 32-char password
		password := resetGeneratePassword(32)

		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("failed to hash password: %w", err)
		}

		// Update the Secret
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		secret.Data["admin.password"] = hash
		// Remove initial password since this is a reset
		delete(secret.Data, "admin.initialPassword")

		if _, err := clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to update Secret: %w", err)
		}

		fmt.Printf("Admin password has been reset.\n")
		fmt.Printf("New password: %s\n", password)
		fmt.Println()
		fmt.Println("WARNING: This password will not be shown again. Store it securely.")

		return nil
	},
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
