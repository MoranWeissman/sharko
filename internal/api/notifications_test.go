package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/notifications"
	"github.com/MoranWeissman/sharko/internal/service"
)

// S3 (walk day 4) — POST /api/v1/notifications/{id}/read.
//
// Pinned contract:
//  1. 200 and the store flips exactly the named notification to read.
//  2. 404 when the id doesn't exist.
//  3. 403 for a viewer — same authz stance as mark-all-read
//     ("reconciler.trigger", operator+).

// notificationsTestServer wires a minimal real Server — these handlers only
// touch s.notificationStore, so no connection/ArgoCD/Git setup is needed
// (unlike reconcileTestServer in clusters_reconcile_test.go).
func notificationsTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	f, err := os.CreateTemp("", "sharko-notifications-test-*.yaml")
	if err != nil {
		t.Fatalf("create temp config file: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	store := config.NewFileStore(f.Name())
	connSvc := service.NewConnectionService(store)
	clusterSvc := service.NewClusterService("")
	addonSvc := service.NewAddonService("")
	dashboardSvc := service.NewDashboardService(connSvc, "")
	observabilitySvc := service.NewObservabilityService(clusterSvc)
	upgradeSvc := service.NewUpgradeService(ai.NewClient(ai.Config{}), nil, "")
	srv := withLegacyOpenAuthForTests(NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, ai.NewClient(ai.Config{})))

	return srv, NewRouter(srv, nil)
}

func notificationsOperatorReq(id string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications/"+id+"/read", nil)
	req.Header.Set("X-Sharko-User", "op")
	req.Header.Set("X-Sharko-Role", "operator")
	return req
}

func TestHandleMarkNotificationRead_200_MarksOnlyThatItem(t *testing.T) {
	srv, router := notificationsTestServer(t)
	srv.NotificationStore().Add(notifications.Notification{
		ID: "1", Title: "A", Type: notifications.TypeUpgrade, Timestamp: time.Now(),
	})
	srv.NotificationStore().Add(notifications.Notification{
		ID: "2", Title: "B", Type: notifications.TypeDrift, Timestamp: time.Now(),
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, notificationsOperatorReq("2"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf(`status = %q, want "ok"`, body["status"])
	}

	if srv.NotificationStore().UnreadCount() != 1 {
		t.Fatalf("expected 1 unread after marking one of two read, got %d", srv.NotificationStore().UnreadCount())
	}
	for _, n := range srv.NotificationStore().List() {
		if n.ID == "2" && !n.Read {
			t.Errorf("expected notification 2 to be Read == true")
		}
		if n.ID == "1" && n.Read {
			t.Errorf("expected notification 1 to remain Read == false")
		}
	}
}

func TestHandleMarkNotificationRead_404_UnknownID(t *testing.T) {
	srv, router := notificationsTestServer(t)
	srv.NotificationStore().Add(notifications.Notification{
		ID: "1", Title: "A", Type: notifications.TypeUpgrade, Timestamp: time.Now(),
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, notificationsOperatorReq("does-not-exist"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleMarkNotificationRead_403_ViewerRole(t *testing.T) {
	srv, router := notificationsTestServer(t)
	srv.NotificationStore().Add(notifications.Notification{
		ID: "1", Title: "A", Type: notifications.TypeUpgrade, Timestamp: time.Now(),
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications/1/read", nil)
	req.Header.Set("X-Sharko-User", "bob")
	req.Header.Set("X-Sharko-Role", "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer role, got %d (body=%s)", w.Code, w.Body.String())
	}
	if srv.NotificationStore().UnreadCount() != 1 {
		t.Errorf("expected the notification to remain unread after a 403, got %d unread", srv.NotificationStore().UnreadCount())
	}
}
