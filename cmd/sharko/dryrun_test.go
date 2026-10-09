package main

import (
	"strings"
	"testing"
)

func TestPrintDryRun_RendersDiffContent(t *testing.T) {
	dr := &cliDryRunResult{
		PRTitle:         "sharko: register cluster prod-eu",
		EffectiveAddons: []string{"cert-manager", "metrics-server"},
		SecretsToCreate: []string{"prod-eu/cert-manager"},
		Verification:    &cliVerification{Success: false, ErrorCode: "ERR_TIMEOUT", ErrorMessage: "no response after 5s"},
		FilesToWrite: []cliFilePreview{
			{
				Path:   "configuration/managed-clusters.yaml",
				Action: "update",
				Diff:   "--- a/configuration/managed-clusters.yaml\n+++ b/configuration/managed-clusters.yaml\n@@ -1,2 +1,3 @@\n clusters:\n+  - name: prod-eu",
			},
		},
	}

	out, _ := captureStdoutT(t, func() error {
		printDryRun(dr)
		return nil
	})

	for _, want := range []string{
		"sharko: register cluster prod-eu",
		"cert-manager, metrics-server",
		"prod-eu/cert-manager",
		"FAILED [ERR_TIMEOUT] no response after 5s",
		"configuration/managed-clusters.yaml",
		"+  - name: prod-eu",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printDryRun output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestPrintDryRun_Nil(t *testing.T) {
	// Must not panic — every command calls this unconditionally on
	// dry-run responses whose dry_run field could theoretically be absent.
	out, _ := captureStdoutT(t, func() error {
		printDryRun(nil)
		return nil
	})
	if out != "" {
		t.Errorf("expected no output for nil dry-run result, got %q", out)
	}
}

func TestPrintAPIError_SurfacesCodeAndProblems(t *testing.T) {
	body := []byte(`{"error":"cluster prod-eu is missing required values","code":"validation_failed","problems":["installCRDs is required","namespace must be set"]}`)
	err := printAPIError(body, 422)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "API error (HTTP 422): cluster prod-eu is missing required values") {
		t.Errorf("first line must keep the original format: %q", msg)
	}
	if !strings.Contains(msg, "code: validation_failed") {
		t.Errorf("expected the code to be surfaced: %q", msg)
	}
	if !strings.Contains(msg, "installCRDs is required") || !strings.Contains(msg, "namespace must be set") {
		t.Errorf("expected both problems to be surfaced: %q", msg)
	}
}

func TestPrintAPIError_NoCodeOrProblems_UnchangedFormat(t *testing.T) {
	body := []byte(`{"error":"cluster not found"}`)
	err := printAPIError(body, 404)
	if err == nil {
		t.Fatal("expected an error")
	}
	want := "API error (HTTP 404): cluster not found"
	if err.Error() != want {
		t.Errorf("got %q, want %q (must not add empty code/problems lines)", err.Error(), want)
	}
}
