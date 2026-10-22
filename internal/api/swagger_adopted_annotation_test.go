package api

import (
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
