package api

// notifications_legacy_leak_test.go — the API half of security story S4.
//
// internal/notifications proves the store cleans a legacy record and writes the
// cleaned state back. This proves the other required thing: that the cleaned
// record is what GET /api/v1/notifications actually serves, through the real
// router, the real handler and the real JSON encoder.
//
// It is a positive control: the sweep is shown to FIND the sentinel in the
// seeded ConfigMap before anything asserts it is absent from the response.

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/notifications"
)

// apiSentinel is shaped like a token a Git backend would echo in an error.
const apiSentinel = "ghp_APISENTINEL0123456789abcdefABCDEFxy"

// apiLeakForms is the same banned-form list the notifications sweep uses: the
// raw value, its base64 forms, its hashes and fragments of it.
func apiLeakForms(secret string) map[string]string {
	sum256 := sha256.Sum256([]byte(secret))
	sum1 := sha1.Sum([]byte(secret))
	sum5 := md5.Sum([]byte(secret))
	return map[string]string{
		"raw value":            secret,
		"base64":               base64.StdEncoding.EncodeToString([]byte(secret)),
		"base64 (url safe)":    base64.URLEncoding.EncodeToString([]byte(secret)),
		"base64 (raw, no pad)": base64.RawStdEncoding.EncodeToString([]byte(secret)),
		"sha256":               hex.EncodeToString(sum256[:]),
		"sha1":                 hex.EncodeToString(sum1[:]),
		"md5":                  hex.EncodeToString(sum5[:]),
		"fragment (first 12)":  secret[:12],
		"fragment (last 12)":   secret[len(secret)-12:],
		"fragment (middle)":    secret[16:32],
	}
}

func apiFindLeaks(text string) []string {
	var found []string
	for name, form := range apiLeakForms(apiSentinel) {
		if strings.Contains(text, form) {
			found = append(found, name)
		}
	}
	return found
}

// TestGetNotifications_NeverServesALegacyRawDetail seeds a ConfigMap in the old
// shape, attaches it to a real Server the way serve.go does, and reads the
// endpoint.
func TestGetNotifications_NeverServesALegacyRawDetail(t *testing.T) {
	srv, router := notificationsTestServer(t)

	// The ConfigMap is seeded with LITERAL BYTES rather than by re-encoding a
	// Go struct, because literal bytes are what an older build actually left
	// behind — and what it left behind has NO `code` key and NO `schema` key.
	// Both fields were added in the same unreleased round, so no shipped Sharko
	// ever wrote either one. Re-encoding today's struct would produce
	// `"code":""` and `"schema":0`, a shape no build ever produced; these are
	// the six keys the released struct really had.
	//
	// The description ends in the Git backend's own words. That is the leak.
	// The id embeds the old TITLE, because that is how the released poller
	// built it (`fmt.Sprintf("connection-%s-%d", title, time.Now().UnixNano())`)
	// — a package constant, so it carries no secret, but it is what the drop
	// warning ends up naming.
	legacyState := `{"notifications":[{` +
		`"id":"connection-` + notifications.TitleGitConnectionBroken + `-1755500000000000000",` +
		`"type":"connection",` +
		`"title":"` + notifications.TitleGitConnectionBroken + `",` +
		`"description":"Sharko uses this Git connection for every commit and pull request, and right now it can't reach it.` +
		` Reason: Get \"https://x-access-token:` + apiSentinel + `@git.example.invalid/o/r\": authentication failed",` +
		`"timestamp":"` + time.Now().UTC().Format(time.RFC3339) + `",` +
		`"read":false` +
		`}]}`

	client := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "sharko-notifications", Namespace: "default"},
		Data:       map[string]string{"state": legacyState},
	})
	cm := cmstore.NewStore(client, "default", "sharko-notifications")

	// POSITIVE CONTROL: the sentinel really is in the stored state right now,
	// and the sweep really does find it. Without this the assertions below
	// prove nothing.
	stored, err := client.CoreV1().ConfigMaps("default").Get(context.Background(), "sharko-notifications", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the seeded configmap: %v", err)
	}
	if got := apiFindLeaks(stored.Data["state"]); len(got) == 0 {
		t.Fatal("POSITIVE CONTROL FAILED: the sweep cannot find the sentinel in the ConfigMap it was just written into.")
	}

	// This is what serve.go does once the in-cluster client is ready.
	if err := srv.SetNotificationCMStore(context.Background(), cm); err != nil {
		t.Fatalf("attaching the notification cmstore: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	req.Header.Set("X-Sharko-User", "someone")
	req.Header.Set("X-Sharko-Role", "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()

	if leaks := apiFindLeaks(body); len(leaks) > 0 {
		t.Errorf("GET /api/v1/notifications served the sentinel as %s\n  body: %s", strings.Join(leaks, ", "), body)
	}

	// The record is GONE rather than repaired. Sharko could not vouch for a
	// description it did not build, so it does not serve one — the connection
	// poller raises the alert again on its next tick if Git is still broken,
	// which is what makes dropping safe rather than lossy.
	if strings.Contains(body, "every commit and pull request") {
		t.Errorf("a record Sharko cannot vouch for was served to the browser anyway:\n%s", body)
	}

	// And the durable state behind the endpoint is clean too, so the next
	// restart has nothing old left to read.
	after, err := client.CoreV1().ConfigMaps("default").Get(context.Background(), "sharko-notifications", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("re-reading the configmap: %v", err)
	}
	if leaks := apiFindLeaks(after.Data["state"]); len(leaks) > 0 {
		t.Errorf("the ConfigMap still holds the sentinel as %s after the server loaded it\n  state: %s",
			strings.Join(leaks, ", "), after.Data["state"])
	}
}
