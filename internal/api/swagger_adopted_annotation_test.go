package api

import (
	"os"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/docs/swagger"
)

// The unadopt endpoint docs must name the real annotation, sharko.dev/adopted.
// The doubled spelling is an old mistake that only lives in the code that
// still reads it.
func TestSwaggerNamesTheRealAdoptedAnnotation(t *testing.T) {
	doc := swagger.SwaggerInfo.ReadDoc()
	if strings.Contains(doc, "sharko.sharko.dev") {
		t.Error("swagger docs mention the doubled sharko.sharko.dev spelling")
	}
	if !strings.Contains(doc, "has sharko.dev/adopted annotation") {
		t.Error("unadopt docs should say the cluster has the sharko.dev/adopted annotation")
	}
}

// plainSwagger returns the swagger text with JSON escapes and line breaks
// folded away, so a sentence can be matched however the generator wrapped it.
func plainSwagger() string {
	doc := strings.ReplaceAll(swagger.SwaggerInfo.ReadDoc(), `\"`, `"`)
	doc = strings.ReplaceAll(doc, `\n`, " ")
	return strings.Join(strings.Fields(doc), " ")
}

// ep3 review G9: the connection update docs spell out the backend save rules.
func TestSwaggerDescribesTheBackendSaveRules(t *testing.T) {
	doc := plainSwagger()
	want := "Secrets backend rules, for both `provider` (cluster credentials) and `addon_secret_provider` (addon secret values): " +
		"a block that is left out keeps the saved one; " +
		"a block whose `type` is empty clears the saved one (for the addon backend that means addon secret values come from the cluster backend again); " +
		"a block with a `type` replaces the saved one, except that when it names the same backend as the saved one (aliases count as the same) and leaves `namespace` or `role_arn` empty, the saved `namespace` and `role_arn` are kept. " +
		"A different backend starts clean."
	if !strings.Contains(doc, want) {
		t.Errorf("PUT /connections/{name} docs do not carry the exact save rules:\n%s", want)
	}
}

// ep3 review G10: a row's Source says "Kubernetes Secrets", as the code does.
func TestSwaggerSourceSentenceMatchesTheCode(t *testing.T) {
	doc := plainSwagger()
	if strings.Contains(doc, `"a Kubernetes Secret"`) {
		t.Error("swagger still says \"a Kubernetes Secret\"; the server says \"Kubernetes Secrets\"")
	}
	if !strings.Contains(doc, `"AWS Secrets Manager", "Kubernetes Secrets", ..., or the honest "secrets store" fallback`) {
		t.Error("swagger does not carry the corrected Source sentence")
	}
}

// ep3 review G7 + G11: the docs say what is true about inline credentials and
// the namespace, and the old wordings stay out.
func TestDocsSayTheTruthAboutInlineAndNamespace(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	cli := read("../../docs/site/cli/commands.md")
	if !strings.Contains(cli, "`inline-kubeconfig` only. This source is off by default. An admin has to turn on \"Allow legacy inline credentials\" in Settings first |") {
		t.Error("cli commands.md does not say inline credentials are off by default")
	}
	if strings.Contains(cli, "An admin can turn this source off in settings") {
		t.Error("cli commands.md still says an admin turns inline off")
	}
	conn := read("../../docs/site/user-guide/connections.md")
	if !strings.Contains(conn, "is not set in Settings, because Settings has no field for it. Set it in the Helm values (`connection.provider.namespace`") {
		t.Error("connections.md does not point the namespace at the Helm values")
	}
	if strings.Contains(conn, "Set the Kubernetes namespace where cluster secrets are stored.") {
		t.Error("connections.md still tells people to set the namespace in Settings")
	}
}
