//go:build e2e

package harness

import (
	"net/http"
	"testing"

	"github.com/MoranWeissman/sharko/internal/api"
	"github.com/MoranWeissman/sharko/internal/notifications"
)

// apiclient_pr.go — typed-client extension for the PR + notification
// surface (V2 Epic 7-1.13).
//
// Reuses the harness Client / generic Get/Post/Delete helpers; only adds
// thin typed wrappers that import the production response shapes from
// internal/api so that any future field rename breaks the harness at
// compile time. Stays consistent with the patterns established by
// apiclient.go's HealthResponse / ListClusters wrappers.

// ListPRs fetches GET /api/v1/prs and returns the typed
// api.PRListResponse. status / cluster / addon / user / operations are
// optional query filters; pass "" / nil to skip a filter.
func (c *Client) ListPRs(t *testing.T) api.PRListResponse {
	t.Helper()
	var out api.PRListResponse
	c.GetJSON(t, "/api/v1/prs", &out)
	return out
}

// GetPR fetches GET /api/v1/prs/{id} and returns the typed api.PRItem.
// 404 is surfaced via t.Fatalf; use Do() for negative-path tests that
// want to assert a specific status.
func (c *Client) GetPR(t *testing.T, id int) api.PRItem {
	t.Helper()
	var out api.PRItem
	c.GetJSON(t, prPath(id), &out)
	return out
}

// RefreshPR POSTs /api/v1/prs/{id}/refresh and returns the freshly
// re-polled api.PRItem (tracker re-queries the upstream Git provider).
func (c *Client) RefreshPR(t *testing.T, id int) api.PRItem {
	t.Helper()
	var out api.PRItem
	c.PostJSON(t, prPath(id)+"/refresh", nil, &out)
	return out
}

// DeletePR issues DELETE /api/v1/prs/{id}. Asserts 200 — the handler
// returns a small {"status":"removed"} body that callers don't need to
// decode.
func (c *Client) DeletePR(t *testing.T, id int) {
	t.Helper()
	c.Delete(t, prPath(id))
}

// ListMergedPRs fetches GET /api/v1/prs/merged and returns the typed
// api.MergedPRsResponse. The handler queries the active GitProvider
// (cached for 60s) so this exercises the harness's MockGitProvider end
// to end.
func (c *Client) ListMergedPRs(t *testing.T) api.MergedPRsResponse {
	t.Helper()
	var out api.MergedPRsResponse
	c.GetJSON(t, "/api/v1/prs/merged", &out)
	return out
}

// NotificationsResponse mirrors the inline shape returned by
// handleListNotifications. The handler deliberately writes a
// map[string]interface{} so the wrapper here uses the typed
// notifications.Notification slice for assertion ergonomics — same
// approach as HealthResponse.
type NotificationsResponse struct {
	Notifications []notifications.Notification `json:"notifications"`
	UnreadCount   int                          `json:"unread_count"`
}

// ListNotifications fetches GET /api/v1/notifications.
func (c *Client) ListNotifications(t *testing.T) NotificationsResponse {
	t.Helper()
	var out NotificationsResponse
	c.GetJSON(t, "/api/v1/notifications", &out)
	return out
}

// MarkAllNotificationsRead POSTs /api/v1/notifications/read-all. The
// handler returns a small {"status":"ok"} body that the helper drops on
// the floor — callers verify the effect by re-listing.
func (c *Client) MarkAllNotificationsRead(t *testing.T) {
	t.Helper()
	c.PostJSON(t, "/api/v1/notifications/read-all", nil, nil, WithExpectStatus(http.StatusOK))
}

// prPath builds /api/v1/prs/<id>. Centralised so the tests can switch
// to a future versioned prefix without sweeping every call site.
func prPath(id int) string {
	return "/api/v1/prs/" + itoa(id)
}

// itoa is a tiny strconv.Itoa shim — keeps the wrappers free of a
// strconv import and signals at the call site that the value is a path
// fragment, not a free-form string.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
