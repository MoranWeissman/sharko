package verify

import "os"

const defaultTestNamespace = "sharko-test"

// TestNamespace returns the namespace used for connectivity tests.
// It reads the SHARKO_TEST_NAMESPACE env var, falling back to "sharko-test".
func TestNamespace() string {
	if ns := os.Getenv("SHARKO_TEST_NAMESPACE"); ns != "" {
		return ns
	}
	return defaultTestNamespace
}
