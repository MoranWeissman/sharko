package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func detectNS() string {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data))
	}
	if ns := os.Getenv("AAP_NAMESPACE"); ns != "" {
		return ns
	}
	return "argocd-addons-platform"
}

const dashboardsCMName = "aap-dashboards"

type embeddedDashboard struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Provider string `json:"provider"` // "datadog", "grafana", "custom"
}

func getK8sClient() (kubernetes.Interface, string, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, "", err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, "", err
	}
	return clientset, detectNS(), nil
}

func loadDashboardsFromK8s(ctx context.Context) ([]embeddedDashboard, error) {
	clientset, ns, err := getK8sClient()
	if err != nil {
		return nil, err
	}
	cm, err := clientset.CoreV1().ConfigMaps(ns).Get(ctx, dashboardsCMName, metav1.GetOptions{})
	if err != nil {
		return []embeddedDashboard{}, nil
	}
	raw, ok := cm.Data["dashboards"]
	if !ok {
		return []embeddedDashboard{}, nil
	}
	var dashboards []embeddedDashboard
	if err := json.Unmarshal([]byte(raw), &dashboards); err != nil {
		return []embeddedDashboard{}, nil
	}
	return dashboards, nil
}

func saveDashboardsToK8s(ctx context.Context, dashboards []embeddedDashboard) error {
	clientset, ns, err := getK8sClient()
	if err != nil {
		return err
	}
	data, _ := json.Marshal(dashboards)

	cm, err := clientset.CoreV1().ConfigMaps(ns).Get(ctx, dashboardsCMName, metav1.GetOptions{})
	if err != nil {
		// Create new ConfigMap
		newCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      dashboardsCMName,
				Namespace: ns,
				Labels:    map[string]string{"app.kubernetes.io/managed-by": "aap"},
			},
			Data: map[string]string{"dashboards": string(data)},
		}
		_, err = clientset.CoreV1().ConfigMaps(ns).Create(ctx, newCM, metav1.CreateOptions{})
		return err
	}
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data["dashboards"] = string(data)
	_, err = clientset.CoreV1().ConfigMaps(ns).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

// API handlers

func (s *Server) handleListDashboards(w http.ResponseWriter, r *http.Request) {
	dashboards, err := loadDashboardsFromK8s(r.Context())
	if err != nil {
		// Fallback: return empty (not in K8s)
		writeJSON(w, http.StatusOK, []embeddedDashboard{})
		return
	}
	writeJSON(w, http.StatusOK, dashboards)
}

func (s *Server) handleSaveDashboards(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var dashboards []embeddedDashboard
	if err := json.NewDecoder(r.Body).Decode(&dashboards); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := saveDashboardsToK8s(r.Context(), dashboards); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save dashboards: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, dashboards)
}
