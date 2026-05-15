//go:build e2e

package harness

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestParseForwardingPort exercises the kubectl-stdout parser used by
// startSharkoPortForward. The :<svcPort> form makes the local port
// random per call, so the parser is on the critical path for finding the
// auth-bundle BaseURL — a regression here would silently route the test
// client at the wrong port (hangs on dial).
//
// Covers:
//   - happy path IPv4
//   - happy path IPv6 (kubectl >= 1.30 binds both stacks)
//   - non-Forwarding info lines (kubectl prints a few resource-lookup
//     lines before the listener is up; we must not return a port from
//     them)
//   - malformed numbers / out-of-range ports
func TestParseForwardingPort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		line    string
		want    int
		wantOK  bool
	}{
		{
			name:   "ipv4 forwarding line",
			line:   "Forwarding from 127.0.0.1:38543 -> 80",
			want:   38543,
			wantOK: true,
		},
		{
			name:   "ipv6 forwarding line",
			line:   "Forwarding from [::1]:38543 -> 80",
			want:   38543,
			wantOK: true,
		},
		{
			name:   "ipv4 with high port",
			line:   "Forwarding from 127.0.0.1:65535 -> 8080",
			want:   65535,
			wantOK: true,
		},
		{
			name:   "ipv4 with low port (still valid)",
			line:   "Forwarding from 127.0.0.1:1024 -> 80",
			want:   1024,
			wantOK: true,
		},
		{
			name:   "info line about resource lookup is not a forwarding line",
			line:   "Handling connection for 38543",
			want:   0,
			wantOK: false,
		},
		{
			name:   "blank line",
			line:   "",
			want:   0,
			wantOK: false,
		},
		{
			name:   "forwarding line with non-loopback IP (we ignore — kubectl normally binds loopback)",
			line:   "Forwarding from 192.168.1.1:38543 -> 80",
			want:   0,
			wantOK: false,
		},
		{
			name:   "port out of range (>65535)",
			line:   "Forwarding from 127.0.0.1:65536 -> 80",
			want:   0,
			wantOK: false,
		},
		{
			name:   "embedded forwarding line (still parses — we match anywhere in the line)",
			line:   "  Forwarding from 127.0.0.1:38543 -> 80",
			want:   38543,
			wantOK: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseForwardingPort(tc.line)
			if ok != tc.wantOK {
				t.Errorf("parseForwardingPort(%q): ok = %v, want %v", tc.line, ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("parseForwardingPort(%q): port = %d, want %d", tc.line, got, tc.want)
			}
		})
	}
}

// TestDecodeBootstrapSecret covers the secret-data decoder used by
// readBootstrapAdminSecret. Split out as a free function specifically so we
// can exercise the data-shape edge cases without a live K8s API:
//
//   - happy path (admin/<password>)
//   - missing username / password keys (broken installer)
//   - empty username / password values (raced installer)
//   - nil .Data (theoretical but possible if a buggy admission webhook strips data)
//   - nil secret pointer (defensive — should never happen in practice)
//
// Without this test the harness would only catch these via "POST
// /auth/login: 401" downstream, which gives an unhelpful diagnostic.
func TestDecodeBootstrapSecret(t *testing.T) {
	t.Parallel()

	mkSecret := func(data map[string][]byte) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      initialAdminSecretName,
				Namespace: "sharko",
			},
			Type: corev1.SecretTypeOpaque,
			Data: data,
		}
	}

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		s := mkSecret(map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("p4ssw0rd-with-trail-space   "),
		})
		user, pass, err := decodeBootstrapSecret(s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if user != "admin" {
			t.Errorf("user = %q, want admin", user)
		}
		// Password must NOT be trimmed — leading/trailing whitespace can
		// be intentional. This guards against a future "helpful"
		// TrimSpace creeping into decodeBootstrapSecret.
		if pass != "p4ssw0rd-with-trail-space   " {
			t.Errorf("pass = %q, want trailing-whitespace preserved", pass)
		}
	})

	t.Run("non-admin username also returned verbatim", func(t *testing.T) {
		t.Parallel()
		s := mkSecret(map[string][]byte{
			"username": []byte("rotated-admin"),
			"password": []byte("hunter2"),
		})
		user, _, err := decodeBootstrapSecret(s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if user != "rotated-admin" {
			t.Errorf("user = %q, want rotated-admin (decoder must not hardcode 'admin')", user)
		}
	})

	t.Run("missing username key", func(t *testing.T) {
		t.Parallel()
		s := mkSecret(map[string][]byte{
			"password": []byte("hunter2"),
		})
		_, _, err := decodeBootstrapSecret(s)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "data.username") {
			t.Errorf("error %q should mention data.username", err)
		}
	})

	t.Run("missing password key", func(t *testing.T) {
		t.Parallel()
		s := mkSecret(map[string][]byte{
			"username": []byte("admin"),
		})
		_, _, err := decodeBootstrapSecret(s)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "data.password") {
			t.Errorf("error %q should mention data.password", err)
		}
	})

	t.Run("empty username value", func(t *testing.T) {
		t.Parallel()
		s := mkSecret(map[string][]byte{
			"username": []byte(""),
			"password": []byte("hunter2"),
		})
		_, _, err := decodeBootstrapSecret(s)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "username") || !strings.Contains(err.Error(), "empty") {
			t.Errorf("error %q should mention username + empty", err)
		}
	})

	t.Run("whitespace-only username treated as empty", func(t *testing.T) {
		t.Parallel()
		s := mkSecret(map[string][]byte{
			"username": []byte("   \t  "),
			"password": []byte("hunter2"),
		})
		_, _, err := decodeBootstrapSecret(s)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("empty password value", func(t *testing.T) {
		t.Parallel()
		s := mkSecret(map[string][]byte{
			"username": []byte("admin"),
			"password": []byte(""),
		})
		_, _, err := decodeBootstrapSecret(s)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "password") || !strings.Contains(err.Error(), "empty") {
			t.Errorf("error %q should mention password + empty", err)
		}
	})

	t.Run("nil .Data", func(t *testing.T) {
		t.Parallel()
		s := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: initialAdminSecretName, Namespace: "sharko"},
			Type:       corev1.SecretTypeOpaque,
			Data:       nil,
		}
		_, _, err := decodeBootstrapSecret(s)
		if err == nil {
			t.Fatalf("expected error on nil .Data, got nil")
		}
		if !strings.Contains(err.Error(), "Data") {
			t.Errorf("error %q should mention .Data", err)
		}
	})

	t.Run("nil secret pointer is a clean error not a panic", func(t *testing.T) {
		t.Parallel()
		_, _, err := decodeBootstrapSecret(nil)
		if err == nil {
			t.Fatalf("expected error on nil secret, got nil")
		}
	})
}

// TestAuthBundleZeroValueFields confirms a zero-valued AuthBundle has the
// shape downstream callers expect (empty strings, not nil-deref panics on
// .BaseURL access). Trivial but cheap insurance: the typed API client takes
// AuthBundle.BaseURL by value and would panic on nil-deref if AuthBundle
// were ever a *struct alias.
func TestAuthBundleZeroValueFields(t *testing.T) {
	t.Parallel()
	var b AuthBundle
	if b.BaseURL != "" || b.Token != "" || b.AdminUser != "" || b.AdminPass != "" {
		t.Errorf("zero-value AuthBundle has non-empty field: %+v", b)
	}
}

// TestBootstrapHelmSharkoAuthRejectsNilHandle covers the input-validation
// guards on bootstrapHelmSharkoAuth. Every misconfiguration here is
// hard-failing rather than degrading — Story 13.3's wrapper SHOULD never
// pass an incomplete *HelmHandle, but if it does we want the diagnostic to
// name the missing field, not silently dial a bogus URL.
func TestBootstrapHelmSharkoAuthRejectsNilHandle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		handle  *HelmHandle
		wantSub string
	}{
		{
			name:    "nil handle",
			handle:  nil,
			wantSub: "helmHandle is nil",
		},
		{
			name: "empty kubeconfig",
			handle: &HelmHandle{
				Namespace: "sharko",
				Service:   "sharko",
			},
			wantSub: "Kubeconfig is empty",
		},
		{
			name: "empty namespace",
			handle: &HelmHandle{
				Kubeconfig: "/tmp/dummy",
				Service:    "sharko",
			},
			wantSub: "Namespace is empty",
		},
		{
			name: "empty service",
			handle: &HelmHandle{
				Kubeconfig: "/tmp/dummy",
				Namespace:  "sharko",
			},
			wantSub: "Service is empty",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := bootstrapHelmSharkoAuth(t, tc.handle)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q should contain %q", err, tc.wantSub)
			}
		})
	}
}
