package api

// docs_ep4_round_s_test.go — pins for the docs lines fixed in ep4 round S.
//
// S4: the docs said "no long-lived key or token is stored" for EKS tokens.
// That is true only when Sharko runs on EKS with its own AWS role. When it
// does not, you give Sharko an AWS access key, and that key IS stored, in a
// Kubernetes Secret. Every copy now says both cases.
//
// S5: the "no AWS login" sentence sent EKS users to a section titled "Give
// Sharko an AWS login when it does not run on EKS". The section is now "Give
// Sharko an AWS login" and has an EKS part about the IRSA annotation.

import (
	"strings"
	"testing"
)

func TestDocsEp4RoundSPins(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	const both = "On EKS, with an AWS role for Sharko's pod (IRSA or EKS Pod Identity), no long-lived AWS key is stored. " +
		"When Sharko does not run on EKS, you give it an AWS access key, and that key is stored in a Kubernetes Secret."

	docs := map[string]string{}
	for _, rel := range []string{
		"docs/site/user-guide/clusters.md",
		"docs/site/user-guide/connections.md",
		"docs/site/api/api-walkthrough.md",
		"docs/site/operator/cluster-connectivity-model.md",
		"docs/site/operator/eks-hub-and-spoke-identity.md",
		"docs/site/operator/configuration.md",
		"docs/site/getting-started/first-run.md",
		"docs/site/getting-started/installation.md",
		"README.md",
	} {
		docs[rel] = flat(readDocForKindPins(t, rel))
	}

	must := []struct{ doc, text string }{
		{"docs/site/user-guide/clusters.md", "Sharko makes a short-lived token from them and renews it before it runs out, so you never paste a cluster token. " + both + " See [Give Sharko an AWS login](../operator/configuration.md#aws-login). EKS clusters only."},
		{"docs/site/user-guide/connections.md", "Sharko makes a short-lived token from them, so you never paste a cluster token. " + both + " See [Give Sharko an AWS login](../operator/configuration.md#aws-login)."},
		{"docs/site/api/api-walkthrough.md", "so you never paste a cluster token. On EKS, with an AWS role for Sharko's pod (IRSA or EKS Pod Identity), no long-lived AWS key is stored. When Sharko does not run on EKS, you give it an AWS access key, and that key is stored in a Kubernetes Secret (see [Give Sharko an AWS login](../operator/configuration.md#aws-login)). You give it the cluster's region."},
		{"docs/site/operator/cluster-connectivity-model.md", "On EKS, with an AWS role for Sharko's pod (IRSA or EKS Pod Identity), no long-lived AWS key is stored, so there is no key to rotate or leak. When Sharko does not run on EKS, you give it an AWS access key, and that key is stored in a Kubernetes Secret (see [Give Sharko an AWS login](configuration.md#aws-login))."},
		{"docs/site/operator/eks-hub-and-spoke-identity.md", "Sharko runs on EKS here, with its own AWS role, so no long-lived AWS key is stored, and there is no key to leak or forget to rotate. If Sharko does not run on EKS, you give it an AWS access key, and that key is stored in a Kubernetes Secret (see [Give Sharko an AWS login](configuration.md#aws-login))."},
		{"docs/site/operator/configuration.md", "### Give Sharko an AWS login {#aws-login}"},
		{"docs/site/operator/configuration.md", "**On EKS.** Give Sharko's pod an AWS role. With IRSA, put the role on Sharko's service account with the `eks.amazonaws.com/role-arn` annotation (`serviceAccount.annotations` in the chart, see [IRSA Setup](#irsa-setup)), then restart Sharko. Or use EKS Pod Identity. The cluster gives the pod its login, and you do not store a key anywhere. If Sharko says it has no AWS login while it runs on EKS, the annotation is most likely missing, or Sharko was not restarted after you added it."},
		// Old links to the off-EKS part still land on it.
		{"docs/site/operator/configuration.md", `<a id="aws-login-outside-eks"></a> **Anywhere else**`},
		{"docs/site/getting-started/first-run.md", "see [Give Sharko an AWS login](../operator/configuration.md#aws-login-outside-eks)."},
		{"docs/site/getting-started/installation.md", "See [Give Sharko an AWS login](../operator/configuration.md#aws-login-outside-eks)."},
		{"docs/site/user-guide/connections.md", "See [Give Sharko an AWS login](../operator/configuration.md#aws-login-outside-eks)."},
	}
	for _, m := range must {
		if !strings.Contains(docs[m.doc], m.text) {
			t.Errorf("%s is missing:\n%s", m.doc, m.text)
		}
	}

	banned := []string{
		"no long-lived key or token is stored",
		"No long-lived key or token is stored",
		"No long-lived key or token is kept",
		"no long-lived credential is stored",
		"you don't store or paste any long-lived credential",
		"Give Sharko an AWS login when it does not run on EKS",
	}
	for rel, d := range docs {
		for _, b := range banned {
			if strings.Contains(d, b) {
				t.Errorf("%s still says %q", rel, b)
			}
		}
	}
}
