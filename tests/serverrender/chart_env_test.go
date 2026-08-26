package serverrender

// chart_env_test.go — the direction nothing guarded: the chart says a
// setting exists, so it had better exist.
//
// # The gap
//
// Two rules already run over SHARKO_ settings, and both stop short of
// the chart:
//
//	registry → reader   internal/envreg/registry_test.go rule 3 opens the
//	                    declared reader and checks the name reaches a real
//	                    environment read.
//	docs → registry     internal/envreg/documented_env_vars_test.go
//	                    refuses a name the documentation claims and the
//	                    registry has never heard of.
//
// Nothing ran chart → registry. charts/ is skipped by the read scan, is
// deliberately NOT a documentation root, no Go test opened
// deployment.yaml, and CI only checked that the chart RENDERS. So a
// reader could be renamed or retired with the chart line left behind and
// everything stayed green — the chart would go on setting a variable
// nothing reads, and every install would carry it.
//
// # Why it renders instead of reading the template
//
// The commit before this one proved why: a break test that moved
// enableServiceLinks inside the container passed a grep over the
// template text and failed the rendered check. A template is not a
// promise; what helm emits is.
//
// # extraEnv
//
// .Values.extraEnv is operator-supplied and unbounded — anything at all
// can be in it. It is excluded from these guards by simply not being set
// in the renders below, which is the honest exclusion: a name an
// operator put there is the operator's own, and the runtime rule in
// internal/envreg/unknown.go is what tells them at startup if they
// misspelled it. A guard that tried to police extraEnv would either
// forbid a supported feature or check nothing.

import (
	"sort"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/envreg"
)

// operatorValues turns on every branch of the chart that an operator can
// reach, so the guard sees every SHARKO_ name the chart can emit rather
// than the handful the defaults happen to render.
//
// The e2e-only value is NOT here — it has its own test below, and its
// own reason.
var operatorValues = []string{
	"--set", "bootstrapAdmin.password=hunter2",
	"--set", "bootstrapAdmin.writeInitialSecret=false",
	"--set", "secrets.webhookSecret=shhh",
	"--set", "clusterRegSource.type=argocd",
	"--set", "clusterRegSource.argocdNamespace=argocd",
	"--set", "connectivityCheck.enabled=false",
	"--set", "autoRemediate.enabled=false",
	"--set", "catalog.freshness.enabled=false",
	"--set", "catalog.freshness.interval=12h",
	"--set", "settings.probeMode=api-test",
	"--set", "settings.allowInlineCredentials=true",
	"--set", "config.environments=prod\\,staging",
	"--set", "gitops.actions.enabled=true",
	"--set", "connection.git.provider=github",
	"--set", "connection.git.repoURL=https://github.com/example/addons",
	"--set", "connection.git.owner=example",
	"--set", "connection.git.repo=addons",
	"--set", "connection.git.organization=example-org",
	"--set", "connection.git.project=example-project",
	"--set", "connection.git.repository=example-repository",
	"--set", "connection.argocd.serverURL=https://argocd.example",
	"--set", "connection.argocd.namespace=argocd",
	"--set", "connection.argocd.insecure=false",
	"--set", "connection.provider.type=aws-sm",
	"--set", "connection.provider.region=eu-west-1",
	"--set", "connection.provider.prefix=clusters/",
	"--set", "connection.provider.namespace=sharko",
	"--set", "connection.provider.roleArn=arn:aws:iam::000000000000:role/example",
	"--set", "connection.addonSecretProvider.type=aws-sm",
	"--set", "connection.addonSecretProvider.region=eu-west-1",
	"--set", "connection.addonSecretProvider.prefix=addons/",
	"--set", "connection.addonSecretProvider.namespace=sharko",
	"--set", "connection.addonSecretProvider.roleArn=arn:aws:iam::000000000000:role/example-addons",
	"--set", "connection.gitops.baseBranch=main",
	"--set", "connection.gitops.branchPrefix=sharko/",
	"--set", "connection.gitops.commitPrefix=sharko:",
	"--set", "connection.gitops.hostClusterName=hub",
	"--set", "connection.gitops.defaultAddons=argocd",
	"--set", "connection.gitops.prAutoMerge=true",
}

// chartSharkoSettings returns every SHARKO_ environment variable name
// the render hands a Sharko container, from both places the chart can
// set one: the container's own env list, and the keys of a Secret the
// container pulls in wholesale with envFrom.
//
// The envFrom half matters. SHARKO_ENCRYPTION_KEY and
// SHARKO_WEBHOOK_SECRET never appear in deployment.yaml at all — they
// are keys of the chart's Secret — so a guard that read only the env
// list would silently check neither.
func chartSharkoSettings(t *testing.T, objects []k8sObject) map[string]string {
	t.Helper()

	secrets := map[string][]string{}
	for _, obj := range objects {
		if obj.Kind != "Secret" {
			continue
		}
		var keys []string
		for k := range obj.Data {
			keys = append(keys, k)
		}
		for k := range obj.StringData {
			keys = append(keys, k)
		}
		secrets[obj.Metadata.Name] = keys
	}

	out := map[string]string{}
	for where, spec := range podSpecs(objects) {
		if !spec.runsSharko() {
			continue
		}
		for _, c := range spec.Containers {
			for _, e := range c.Env {
				if strings.HasPrefix(e.Name, envreg.SettingPrefix) {
					out[e.Name] = where + " container " + c.Name + " env"
				}
			}
			for _, from := range c.EnvFrom {
				if from.SecretRef.Name == "" {
					continue
				}
				for _, key := range secrets[from.SecretRef.Name] {
					if strings.HasPrefix(key, envreg.SettingPrefix) {
						out[key] = where + " container " + c.Name + " envFrom Secret/" + from.SecretRef.Name
					}
				}
			}
		}
	}
	return out
}

// chartSettingsWithNoRealSetting is the rule itself, as a function over
// names so it can be driven with inputs that are not the shipped chart.
//
// A rule that has only ever been run over a chart that satisfies it has
// never executed its own comparison. TestTheChartRuleFires below hands
// it names that should fail.
func chartSettingsWithNoRealSetting(names map[string]string, allowInternal bool) []string {
	var findings []string
	for name, where := range names {
		s, registered := envreg.Get(name)
		if !registered {
			findings = append(findings, name+" ("+where+") is not in the configuration registry at all")
			continue
		}
		switch s.Kind {
		case envreg.Production:
		case envreg.Internal:
			if !allowInternal {
				findings = append(findings, name+" ("+where+") is registered as internal — not an "+
					"operator-facing setting, so the chart should not be setting it on an ordinary install")
			}
		default:
			findings = append(findings, name+" ("+where+") is registered as "+string(s.Kind)+
				", which is not something the shipped chart may set")
		}
		if s.ReaderFile == "" {
			findings = append(findings, name+" ("+where+") names no reader")
			continue
		}
		rel := strings.TrimSpace(s.ReaderFile)
		if strings.HasSuffix(rel, "_test.go") || strings.HasPrefix(rel, "tests/") || strings.HasPrefix(rel, "scripts/") {
			findings = append(findings, name+" ("+where+") names reader "+rel+
				", which is test or script code — the chart is setting it on a real install")
		}
	}
	sort.Strings(findings)
	return findings
}

// TestEverySharkoSettingTheChartSetsIsReal is the guard.
//
// It is the only thing standing between "somebody retired a reader" and
// "every install goes on setting a variable nothing reads". Since the
// commit that added the unknown-setting rule it is stronger than that:
// a chart line naming a setting the registry does not know now stops
// every install from booting.
func TestEverySharkoSettingTheChartSetsIsReal(t *testing.T) {
	objects := renderServerChart(t, operatorValues...)
	names := chartSharkoSettings(t, objects)

	if len(names) < 25 {
		t.Fatalf("only %d SHARKO_ settings were found in the render: %v\n\n"+
			"The chart sets far more than that with these values. The scan is broken, not the chart — "+
			"and a broken scan reports ok forever.", len(names), sortedKeys(names))
	}

	// The two that only reach the container through envFrom. If the scan
	// ever stops reading the Secret it goes quiet rather than failing,
	// so they are pinned by name.
	for _, name := range []string{"SHARKO_ENCRYPTION_KEY", "SHARKO_WEBHOOK_SECRET"} {
		if _, found := names[name]; !found {
			t.Errorf("%s was not seen. It is a key of the chart's Secret, pulled in with envFrom — "+
				"if the scan no longer reads those, it is checking only half the chart.", name)
		}
	}

	if findings := chartSettingsWithNoRealSetting(names, false); len(findings) > 0 {
		t.Errorf("%d setting(s) the chart sets have no real setting behind them:\n\n  %s\n\n"+
			"Either register the name in internal/envreg/registry.go with the file that reads it, or "+
			"take the line out of the chart. A chart line with no reader is a variable every install "+
			"carries and nothing looks at — and since the unknown-setting rule landed, it is a "+
			"variable that stops the server.",
			len(findings), strings.Join(findings, "\n  "))
	}
}

// TestTheE2EOnlyChartSettingIsRegisteredToo covers the one branch left
// out of the render above.
//
// e2e.gitHostsAllowlist emits SHARKO_E2E_GIT_HOSTS_ALLOWLIST, which the
// registry classifies as internal — production code reads it, but it is
// not an operator knob, and its kind is marked provisional pending a
// product ruling. So it is checked with internal allowed, and separately,
// so the main guard above stays able to say "the chart must not set an
// internal name on an ordinary install".
func TestTheE2EOnlyChartSettingIsRegisteredToo(t *testing.T) {
	withE2E := append(append([]string{}, operatorValues...),
		"--set", "e2e.gitHostsAllowlist=gitfake.sharko-e2e.svc.cluster.local")
	names := chartSharkoSettings(t, renderServerChart(t, withE2E...))

	if _, found := names["SHARKO_E2E_GIT_HOSTS_ALLOWLIST"]; !found {
		t.Fatal("setting e2e.gitHostsAllowlist did not put SHARKO_E2E_GIT_HOSTS_ALLOWLIST in the " +
			"render, so this test is checking nothing")
	}
	if findings := chartSettingsWithNoRealSetting(names, true); len(findings) > 0 {
		t.Errorf("%d setting(s) the chart sets have no real setting behind them:\n\n  %s",
			len(findings), strings.Join(findings, "\n  "))
	}

	// And the ordinary render does NOT carry it — otherwise the split
	// above is decoration.
	ordinary := chartSharkoSettings(t, renderServerChart(t, operatorValues...))
	if _, found := ordinary["SHARKO_E2E_GIT_HOSTS_ALLOWLIST"]; found {
		t.Error("an ordinary install carries the e2e-only setting. It is meant to be emitted only " +
			"when a test rig asks for it.")
	}
}

// TestTheChartRuleFires drives the rule over names that are not the
// shipped chart's, because a rule that has never once returned a
// finding is a rule nobody has checked.
func TestTheChartRuleFires(t *testing.T) {
	const where = "Deployment/sharko container sharko env"
	for _, tc := range []struct {
		what          string
		names         map[string]string
		allowInternal bool
		wantRefused   bool
	}{
		{
			what:        "a chart line naming a setting nothing reads",
			names:       map[string]string{"SHARKO_RETIRED_KNOB": where},
			wantRefused: true,
		},
		{
			what:        "a chart line naming a test-harness setting",
			names:       map[string]string{"SHARKO_E2E_IMAGE_TAG": where},
			wantRefused: true,
		},
		{
			what:        "a chart line naming an internal setting on an ordinary install",
			names:       map[string]string{"SHARKO_DEV_MODE": where},
			wantRefused: true,
		},
		{
			what:          "the same internal setting where internal is allowed",
			names:         map[string]string{"SHARKO_DEV_MODE": where},
			allowInternal: true,
		},
		{
			what:  "an ordinary production setting",
			names: map[string]string{"SHARKO_LOG_LEVEL": where},
		},
	} {
		got := chartSettingsWithNoRealSetting(tc.names, tc.allowInternal)
		if refused := len(got) > 0; refused != tc.wantRefused {
			t.Errorf("%s: refused=%v, want %v — %v", tc.what, refused, tc.wantRefused, got)
		}
		for _, finding := range got {
			for name := range tc.names {
				if !strings.Contains(finding, name) {
					t.Errorf("%s: a finding does not name the setting: %q", tc.what, finding)
				}
			}
		}
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
