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

// newUpgradeNotification and newDriftNotification build real notifications
// through the boundary in internal/notifications: a caller names the subject
// and the server renders the id, title, description and type.
func newUpgradeNotification(addon string) notifications.Notification {
	return notifications.New(notifications.CodeAddonUpgradeAvailable, "", notifications.Params{
		Addon: addon, Version: "2.0.0", CatalogVersion: "1.0.0",
	}, time.Now())
}

func newDriftNotification(addon string) notifications.Notification {
	return notifications.New(notifications.CodeAddonVersionDrift, "", notifications.Params{
		Addon: addon, Cluster: "prod-eu", Version: "2.0.0", CatalogVersion: "1.0.0",
	}, time.Now())
}

func notificationsOperatorReq(id string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications/"+id+"/read", nil)
	req.Header.Set("X-Sharko-User", "op")
	req.Header.Set("X-Sharko-Role", "operator")
	return req
}

func TestHandleMarkNotificationRead_200_MarksOnlyThatItem(t *testing.T) {
	srv, router := notificationsTestServer(t)
	// A notification's id, title and type are written by the server from its
	// code and a set of checked identifiers, so these are built through
	// notifications.New and the ids are read back off the values.
	first := newUpgradeNotification("first")
	second := newDriftNotification("second")
	srv.NotificationStore().Add(first)
	srv.NotificationStore().Add(second)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, notificationsOperatorReq(second.ID))

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
		if n.ID == second.ID && !n.Read {
			t.Errorf("expected notification %q to be Read == true", n.ID)
		}
		if n.ID == first.ID && n.Read {
			t.Errorf("expected notification %q to remain Read == false", n.ID)
		}
	}
}

func TestHandleMarkNotificationRead_404_UnknownID(t *testing.T) {
	srv, router := notificationsTestServer(t)
	srv.NotificationStore().Add(newUpgradeNotification("first"))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, notificationsOperatorReq("does-not-exist"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestHandleMarkNotificationRead_403_ViewerRole(t *testing.T) {
	srv, router := notificationsTestServer(t)
	only := newUpgradeNotification("first")
	srv.NotificationStore().Add(only)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/notifications/"+only.ID+"/read", nil)
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
