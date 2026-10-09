package api

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/MoranWeissman/sharko/internal/authz"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// handleCheckResourceExclusions godoc
//
// @Summary Check ArgoCD resource exclusions
// @Description Reads the argocd-cm ConfigMap and checks whether the Sharko-managed
// @Description Secret exclusion label (app.kubernetes.io/managed-by: sharko) is
// @Description configured. Returns configured=true when the exclusion is present, plus
// @Description a human-readable recommendation when it is missing.
// @Tags system
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Exclusion status"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /argocd/resource-exclusions [get]
func (s *Server) handleCheckResourceExclusions(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "argocd.resource-exclusions") {
		return
	}

	configured, detail := checkArgoCDResourceExclusions(r.Context())
	resp := map[string]interface{}{
		"configured": configured,
		"detail":     detail,
	}
	if !configured {
		resp["recommendation"] = "Add the following to your argocd-cm ConfigMap so ArgoCD does not manage or delete Sharko-provisioned secrets:\n\n" +
			"resource.exclusions: |\n" +
			"  - apiGroups: [\"\"]\n" +
			"    kinds: [\"Secret\"]\n" +
			"    clusters: [\"*\"]\n" +
			"    labelSelector:\n" +
			"      matchLabels:\n" +
			"        app.kubernetes.io/managed-by: sharko\n\n" +
			"See the Sharko operator documentation for full details."
	}
	writeJSON(w, http.StatusOK, resp)
}

// checkArgoCDResourceExclusions reads argocd-cm and tests whether the sharko
// label exclusion is present. It returns (true, detail) when configured and
// (false, reason) when not configured or when the check cannot be performed
// (e.g. not running in-cluster).
func checkArgoCDResourceExclusions(ctx context.Context) (bool, string) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// Not running in-cluster (local dev); report unknown.
		return false, "not running in-cluster — cannot read argocd-cm; configure resource exclusions manually"
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return false, "could not create Kubernetes client: " + err.Error()
	}

	namespace := os.Getenv("ARGOCD_NAMESPACE")
	if namespace == "" {
		namespace = "argocd"
	}

	cm, err := client.CoreV1().ConfigMaps(namespace).Get(ctx, "argocd-cm", metav1.GetOptions{})
	if err != nil {
		return false, "could not read argocd-cm in namespace " + namespace + ": " + err.Error()
	}

	exclusions, ok := cm.Data["resource.exclusions"]
	if !ok || exclusions == "" {
		return false, "argocd-cm has no resource.exclusions configured"
	}

	// Look for a mention of both the sharko label and Secrets in the exclusions block.
	lower := strings.ToLower(exclusions)
	if strings.Contains(lower, "sharko") && strings.Contains(lower, "secret") {
		return true, "resource.exclusions is configured with a sharko Secret exclusion"
	}

	return false, "resource.exclusions exists but does not include a sharko Secret exclusion"
}
