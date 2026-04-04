// Package demo provides a full in-memory mock backend for QA testing.
// Start it with: sharko serve --demo
package demo

// Cluster represents a demo cluster definition.
type Cluster struct {
	Name          string
	Server        string
	Region        string
	Env           string
	K8sVersion    string
	ConnStatus    string // "Successful", "Failed"
	Addons        map[string]string // addon name → chart version installed
}

// Addon represents a demo addon definition from the catalog.
type Addon struct {
	Name       string
	ChartName  string
	RepoURL    string
	LatestVersion string
	Description string
}

// demoClusters is the seed data for the 5 registered clusters.
var demoClusters = []Cluster{
	{
		Name:       "prod-eu",
		Server:     "https://k8s.prod-eu.demo.example.com",
		Region:     "eu-west-1",
		Env:        "production",
		K8sVersion: "1.29.3",
		ConnStatus: "Successful",
		Addons: map[string]string{
			"cert-manager":           "1.14.4",
			"metrics-server":         "3.12.1",
			"kube-prometheus-stack":  "58.1.3",
			"external-dns":           "1.14.4",
			"istio-base":             "1.21.0",
		},
	},
	{
		Name:       "prod-us",
		Server:     "https://k8s.prod-us.demo.example.com",
		Region:     "us-east-1",
		Env:        "production",
		K8sVersion: "1.29.3",
		ConnStatus: "Successful",
		Addons: map[string]string{
			"cert-manager":           "1.14.4",
			"metrics-server":         "3.12.1",
			"kube-prometheus-stack":  "58.1.3",
			"external-dns":           "1.14.4",
		},
	},
	{
		Name:       "staging-eu",
		Server:     "https://k8s.staging-eu.demo.example.com",
		Region:     "eu-west-1",
		Env:        "staging",
		K8sVersion: "1.28.7",
		ConnStatus: "Successful",
		Addons: map[string]string{
			"cert-manager":           "1.13.6", // older version — will show drift
			"metrics-server":         "3.11.0",
			"kube-prometheus-stack":  "57.2.0",
			"datadog":                "3.68.0",
		},
	},
	{
		Name:       "dev-us",
		Server:     "https://k8s.dev-us.demo.example.com",
		Region:     "us-west-2",
		Env:        "development",
		K8sVersion: "1.28.7",
		ConnStatus: "Successful",
		Addons: map[string]string{
			"cert-manager":  "1.13.6",
			"metrics-server": "3.12.1",
			"vault":         "0.27.0",
		},
	},
	{
		Name:       "perf-asia",
		Server:     "https://k8s.perf-asia.demo.example.com",
		Region:     "ap-southeast-1",
		Env:        "performance",
		K8sVersion: "1.27.12",
		ConnStatus: "Failed", // degraded cluster for demo
		Addons: map[string]string{
			"cert-manager":           "1.12.9",
			"metrics-server":         "3.10.0",
			"kube-prometheus-stack":  "55.5.0",
		},
	},
}

// demoAddons is the seed data for the addon catalog.
var demoAddons = []Addon{
	{
		Name:          "cert-manager",
		ChartName:     "cert-manager",
		RepoURL:       "https://charts.jetstack.io",
		LatestVersion: "1.14.4",
		Description:   "X.509 certificate management for Kubernetes",
	},
	{
		Name:          "metrics-server",
		ChartName:     "metrics-server",
		RepoURL:       "https://kubernetes-sigs.github.io/metrics-server/",
		LatestVersion: "3.12.1",
		Description:   "Scalable and efficient source of container resource metrics",
	},
	{
		Name:          "datadog",
		ChartName:     "datadog",
		RepoURL:       "https://helm.datadoghq.com",
		LatestVersion: "3.69.0",
		Description:   "Datadog monitoring agent for Kubernetes",
	},
	{
		Name:          "external-dns",
		ChartName:     "external-dns",
		RepoURL:       "https://kubernetes-sigs.github.io/external-dns/",
		LatestVersion: "1.14.4",
		Description:   "Synchronizes exposed Kubernetes Services and Ingresses with DNS providers",
	},
	{
		Name:          "istio-base",
		ChartName:     "base",
		RepoURL:       "https://istio-release.storage.googleapis.com/charts",
		LatestVersion: "1.21.1",
		Description:   "Istio service mesh base components",
	},
	{
		Name:          "kube-prometheus-stack",
		ChartName:     "kube-prometheus-stack",
		RepoURL:       "https://prometheus-community.github.io/helm-charts",
		LatestVersion: "58.2.1",
		Description:   "Kubernetes monitoring stack: Prometheus, Grafana, Alertmanager",
	},
	{
		Name:          "logging-operator",
		ChartName:     "logging-operator",
		RepoURL:       "https://kube-logging.github.io/helm-charts",
		LatestVersion: "4.6.0",
		Description:   "Kubernetes logging operator for Fluentd and Fluent Bit",
	},
	{
		Name:          "vault",
		ChartName:     "vault",
		RepoURL:       "https://helm.releases.hashicorp.com",
		LatestVersion: "0.28.0",
		Description:   "HashiCorp Vault secrets management",
	},
}

// demoUnregisteredClusters are clusters visible to the provider but not yet in ArgoCD.
var demoUnregisteredClusters = []Cluster{
	{
		Name:       "dr-eu",
		Server:     "https://k8s.dr-eu.demo.example.com",
		Region:     "eu-central-1",
		Env:        "disaster-recovery",
		K8sVersion: "1.29.3",
		ConnStatus: "Successful",
	},
	{
		Name:       "sandbox-us",
		Server:     "https://k8s.sandbox-us.demo.example.com",
		Region:     "us-east-2",
		Env:        "sandbox",
		K8sVersion: "1.28.7",
		ConnStatus: "Successful",
	},
}
