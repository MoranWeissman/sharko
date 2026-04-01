package datadog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// Config holds Datadog API configuration.
type Config struct {
	APIKey string
	AppKey string
	Site   string // datadoghq.com or datadoghq.eu
}

// Client is a Datadog API client.
type Client struct {
	config Config
	http   *http.Client
}

// NewClient creates a new Datadog client.
func NewClient(cfg Config) *Client {
	if cfg.Site == "" {
		cfg.Site = "datadoghq.com"
	}
	return &Client{config: cfg, http: &http.Client{Timeout: 30 * time.Second}}
}

// IsEnabled returns true if both API and App keys are configured.
func (c *Client) IsEnabled() bool {
	return c.config.APIKey != "" && c.config.AppKey != ""
}

// Site returns the configured Datadog site.
func (c *Client) Site() string {
	return c.config.Site
}

// MetricPoint represents a single data point.
type MetricPoint struct {
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

// MetricSeries represents a time series with tags.
type MetricSeries struct {
	Metric     string            `json:"metric"`
	Tags       map[string]string `json:"tags"`
	Points     []MetricPoint     `json:"points"`
	Expression string            `json:"expression,omitempty"`
}

// QueryMetrics queries Datadog Metrics API.
// query examples:
//
//	"avg:container.cpu.usage{kube_namespace:datadog}"
//	"sum:container.memory.usage{kube_namespace:istio-system} by {pod_name}"
func (c *Client) QueryMetrics(ctx context.Context, query string, from, to time.Time) ([]MetricSeries, error) {
	baseURL := fmt.Sprintf("https://api.%s/api/v1/query", c.config.Site)

	params := url.Values{
		"query": {query},
		"from":  {fmt.Sprintf("%d", from.Unix())},
		"to":    {fmt.Sprintf("%d", to.Unix())},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("DD-API-KEY", c.config.APIKey)
	req.Header.Set("DD-APPLICATION-KEY", c.config.AppKey)

	resp, err := c.http.Do(req)
	if err != nil {
		slog.Error("datadog query failed", "error", err, "query", query)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		slog.Error("datadog query failed", "status", resp.StatusCode, "query", query)
		return nil, fmt.Errorf("datadog returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Series []struct {
			Metric    string      `json:"metric"`
			TagSet    []string    `json:"tag_set"`
			Pointlist [][]float64 `json:"pointlist"`
		} `json:"series"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	var series []MetricSeries
	for _, s := range result.Series {
		ms := MetricSeries{
			Metric: s.Metric,
			Tags:   parseTags(s.TagSet),
		}
		for _, p := range s.Pointlist {
			if len(p) >= 2 {
				ms.Points = append(ms.Points, MetricPoint{
					Timestamp: int64(p[0]) / 1000, // Datadog returns milliseconds
					Value:     p[1],
				})
			}
		}
		series = append(series, ms)
	}

	return series, nil
}

// Validate checks if the API keys are valid.
func (c *Client) Validate(ctx context.Context) error {
	u := fmt.Sprintf("https://api.%s/api/v1/validate", c.config.Site)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("DD-API-KEY", c.config.APIKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		Valid bool `json:"valid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	if !result.Valid {
		return fmt.Errorf("invalid API key")
	}
	return nil
}

// NamespaceMetrics holds aggregated metrics for a Kubernetes namespace.
type NamespaceMetrics struct {
	Namespace         string  `json:"namespace"`
	CPUUsageNanoCores float64 `json:"cpu_usage_nanocores"`
	CPUUsageCores     float64 `json:"cpu_usage_cores"`
	MemoryUsageBytes  float64 `json:"memory_usage_bytes"`
	MemoryUsageMB     float64 `json:"memory_usage_mb"`
	RunningPods       int     `json:"running_pods"`
}

// GetNamespaceMetrics fetches CPU and memory metrics for a namespace.
func (c *Client) GetNamespaceMetrics(ctx context.Context, namespace string, duration time.Duration) (*NamespaceMetrics, error) {
	now := time.Now()
	from := now.Add(-duration)

	metrics := &NamespaceMetrics{Namespace: namespace}

	// CPU usage
	cpuSeries, err := c.QueryMetrics(ctx,
		fmt.Sprintf("avg:container.cpu.usage{kube_namespace:%s}", namespace),
		from, now)
	if err == nil && len(cpuSeries) > 0 && len(cpuSeries[0].Points) > 0 {
		last := cpuSeries[0].Points[len(cpuSeries[0].Points)-1]
		metrics.CPUUsageNanoCores = last.Value
		metrics.CPUUsageCores = last.Value / 1e9 // convert nanocores to cores
	}

	// Memory usage
	memSeries, err := c.QueryMetrics(ctx,
		fmt.Sprintf("avg:container.memory.usage{kube_namespace:%s}", namespace),
		from, now)
	if err == nil && len(memSeries) > 0 && len(memSeries[0].Points) > 0 {
		last := memSeries[0].Points[len(memSeries[0].Points)-1]
		metrics.MemoryUsageBytes = last.Value
		metrics.MemoryUsageMB = last.Value / (1024 * 1024)
	}

	// Pod count
	podSeries, err := c.QueryMetrics(ctx,
		fmt.Sprintf("sum:kubernetes.pods.running{kube_namespace:%s}", namespace),
		from, now)
	if err == nil && len(podSeries) > 0 && len(podSeries[0].Points) > 0 {
		last := podSeries[0].Points[len(podSeries[0].Points)-1]
		metrics.RunningPods = int(last.Value)
	}

	return metrics, nil
}

func parseTags(tagSet []string) map[string]string {
	tags := make(map[string]string)
	for _, t := range tagSet {
		parts := splitTag(t)
		if len(parts) == 2 {
			tags[parts[0]] = parts[1]
		}
	}
	return tags
}

func splitTag(tag string) []string {
	for i, c := range tag {
		if c == ':' {
			return []string{tag[:i], tag[i+1:]}
		}
	}
	return []string{tag}
}

// AddonMetrics holds usage vs requests vs limits for one addon on one cluster.
type AddonMetrics struct {
	AddonName       string  `json:"addon_name"`
	Namespace       string  `json:"namespace"`
	CPUUsageCores   float64 `json:"cpu_usage_cores"`
	CPURequestCores float64 `json:"cpu_request_cores"`
	CPULimitCores   float64 `json:"cpu_limit_cores"`
	MemUsageMB      float64 `json:"mem_usage_mb"`
	MemRequestMB    float64 `json:"mem_request_mb"`
	MemLimitMB      float64 `json:"mem_limit_mb"`
	PodCount        int     `json:"pod_count"`
}

// ClusterMetrics holds metrics for all addons on a cluster.
type ClusterMetrics struct {
	ClusterName string         `json:"cluster_name"`
	Addons      []AddonMetrics `json:"addons"`
}

// GetClusterAddonMetrics fetches metrics for addons on a specific cluster.
// Uses kube_cluster_name tag to filter by cluster, and kube_namespace to group by addon.
func (c *Client) GetClusterAddonMetrics(ctx context.Context, clusterName string, namespaces []string, duration time.Duration) (*ClusterMetrics, error) {
	now := time.Now()
	from := now.Add(-duration)

	result := &ClusterMetrics{ClusterName: clusterName}
	slog.Info("datadog fetching cluster metrics", "cluster", clusterName, "namespaces", len(namespaces))

	for _, ns := range namespaces {
		addon := AddonMetrics{AddonName: ns, Namespace: ns}

		// CPU usage (actual)
		q := fmt.Sprintf("avg:container.cpu.usage{kube_namespace:%s,kube_cluster_name:%s}", ns, clusterName)
		series, _ := c.QueryMetrics(ctx, q, from, now)
		if len(series) > 0 && len(series[0].Points) > 0 {
			addon.CPUUsageCores = series[0].Points[len(series[0].Points)-1].Value / 1e9
		}

		// CPU requests
		q = fmt.Sprintf("sum:kubernetes.cpu.requests{kube_namespace:%s,kube_cluster_name:%s}", ns, clusterName)
		series, _ = c.QueryMetrics(ctx, q, from, now)
		if len(series) > 0 && len(series[0].Points) > 0 {
			addon.CPURequestCores = series[0].Points[len(series[0].Points)-1].Value / 1e9
		}

		// CPU limits
		q = fmt.Sprintf("sum:kubernetes.cpu.limits{kube_namespace:%s,kube_cluster_name:%s}", ns, clusterName)
		series, _ = c.QueryMetrics(ctx, q, from, now)
		if len(series) > 0 && len(series[0].Points) > 0 {
			addon.CPULimitCores = series[0].Points[len(series[0].Points)-1].Value / 1e9
		}

		// Memory usage
		q = fmt.Sprintf("avg:container.memory.usage{kube_namespace:%s,kube_cluster_name:%s}", ns, clusterName)
		series, _ = c.QueryMetrics(ctx, q, from, now)
		if len(series) > 0 && len(series[0].Points) > 0 {
			addon.MemUsageMB = series[0].Points[len(series[0].Points)-1].Value / (1024 * 1024)
		}

		// Memory requests
		q = fmt.Sprintf("sum:kubernetes.memory.requests{kube_namespace:%s,kube_cluster_name:%s}", ns, clusterName)
		series, _ = c.QueryMetrics(ctx, q, from, now)
		if len(series) > 0 && len(series[0].Points) > 0 {
			addon.MemRequestMB = series[0].Points[len(series[0].Points)-1].Value / (1024 * 1024)
		}

		// Memory limits
		q = fmt.Sprintf("sum:kubernetes.memory.limits{kube_namespace:%s,kube_cluster_name:%s}", ns, clusterName)
		series, _ = c.QueryMetrics(ctx, q, from, now)
		if len(series) > 0 && len(series[0].Points) > 0 {
			addon.MemLimitMB = series[0].Points[len(series[0].Points)-1].Value / (1024 * 1024)
		}

		// Running pods (current)
		q = fmt.Sprintf("sum:kubernetes.pods.running{kube_namespace:%s,kube_cluster_name:%s}", ns, clusterName)
		series, _ = c.QueryMetrics(ctx, q, from, now)
		if len(series) > 0 && len(series[0].Points) > 0 {
			addon.PodCount = int(series[0].Points[len(series[0].Points)-1].Value)
		}

		result.Addons = append(result.Addons, addon)
	}

	slog.Info("datadog cluster metrics fetched", "cluster", clusterName, "addons", len(result.Addons))
	return result, nil
}
