// addon_secret_defs_demo.go — S1: addon-values secret definitions for a
// generated (non-default) demo estate, chosen from addons the generator's
// BigScaleConfig selection actually includes (internal/demo/generator.go's
// selectAddons draws from the real curated catalog, catalog/addons.yaml —
// verified against the deterministic BigSeed selection: cert-manager,
// cloudnative-pg, external-secrets, kube-prometheus-stack, and loki are all
// in it). Names, namespaces, and key maps below are plausible placeholders
// pointing at fake vault paths — no real secret value, ever.
package demo

import "github.com/MoranWeissman/sharko/internal/orchestrator"

// demoGeneratedAddonSecretDefs returns the additional addon-values secret
// definitions used ONLY for a generated (non-default) demo estate — see
// SetupDemoServer step 6 for why these are gated behind estate != nil
// rather than merged into the base datadog/vault definitions unconditionally.
func demoGeneratedAddonSecretDefs() map[string]orchestrator.AddonSecretDefinition {
	return map[string]orchestrator.AddonSecretDefinition{
		"kube-prometheus-stack": {
			AddonName:  "kube-prometheus-stack",
			SecretName: "kube-prometheus-stack-grafana-admin",
			Namespace:  "monitoring",
			Keys: map[string]string{
				"admin-password": "secrets/kube-prometheus-stack/grafana-admin-password",
			},
		},
		"loki": {
			AddonName:  "loki",
			SecretName: "loki-object-storage",
			Namespace:  "monitoring",
			Keys: map[string]string{
				"access-key-id":     "secrets/loki/object-storage-access-key-id",
				"secret-access-key": "secrets/loki/object-storage-secret-access-key",
			},
		},
		"external-secrets": {
			AddonName:  "external-secrets",
			SecretName: "external-secrets-webhook-certs",
			Namespace:  "external-secrets",
			Keys: map[string]string{
				"tls.crt": "secrets/external-secrets/webhook-tls-cert",
				"tls.key": "secrets/external-secrets/webhook-tls-key",
			},
		},
		"cloudnative-pg": {
			AddonName:  "cloudnative-pg",
			SecretName: "cloudnative-pg-superuser",
			Namespace:  "cnpg-system",
			Keys: map[string]string{
				"password": "secrets/cloudnative-pg/superuser-password",
			},
		},
		"cert-manager": {
			AddonName:  "cert-manager",
			SecretName: "cert-manager-dns01-credentials",
			Namespace:  "cert-manager",
			Keys: map[string]string{
				"api-token": "secrets/cert-manager/dns01-api-token",
			},
		},
	}
}
