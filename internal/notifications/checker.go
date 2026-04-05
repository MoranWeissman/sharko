package notifications

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// VersionInfo contains addon version data used by the Checker.
type VersionInfo struct {
	AddonName       string
	CatalogVersion  string
	ClusterVersions map[string]string // cluster name -> deployed version
	LatestVersion   string            // from Helm repo
}

// VersionProvider is the interface the Checker consults each cycle.
type VersionProvider interface {
	GetVersionInfo(ctx context.Context) ([]VersionInfo, error)
}

// Checker runs periodically and pushes notifications into the Store based on
// version drift and available upgrades detected via a VersionProvider.
type Checker struct {
	store    *Store
	provider VersionProvider
	interval time.Duration
	stopCh   chan struct{}
}

// NewChecker creates a Checker that is not yet running.
func NewChecker(store *Store, provider VersionProvider, interval time.Duration) *Checker {
	return &Checker{
		store:    store,
		provider: provider,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start launches the background goroutine. It runs one check immediately, then
// repeats on the configured interval until Stop is called.
func (c *Checker) Start() {
	go func() {
		// Run immediately on start.
		c.check()

		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				c.check()
			case <-c.stopCh:
				return
			}
		}
	}()
}

// Stop signals the background goroutine to exit.
func (c *Checker) Stop() {
	close(c.stopCh)
}

func (c *Checker) check() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	infos, err := c.provider.GetVersionInfo(ctx)
	if err != nil {
		log.Printf("[notifications] check failed: %v", err)
		return
	}

	now := time.Now()

	for _, info := range infos {
		// Check for upgrades (major/minor only).
		if info.LatestVersion != "" && info.LatestVersion != info.CatalogVersion {
			if isMajorOrMinorUpgrade(info.CatalogVersion, info.LatestVersion) {
				c.store.Add(Notification{
					ID:          fmt.Sprintf("upgrade-%s-%s", info.AddonName, info.LatestVersion),
					Type:        TypeUpgrade,
					Title:       fmt.Sprintf("%s %s available", info.AddonName, info.LatestVersion),
					Description: fmt.Sprintf("Upgrade from %s to %s", info.CatalogVersion, info.LatestVersion),
					Timestamp:   now,
				})
			}
		}

		// Check for drift between cluster version and catalog version.
		for clusterName, clusterVersion := range info.ClusterVersions {
			if clusterVersion != info.CatalogVersion && clusterVersion != "" {
				c.store.Add(Notification{
					ID:          fmt.Sprintf("drift-%s-%s", info.AddonName, clusterName),
					Type:        TypeDrift,
					Title:       fmt.Sprintf("Version drift: %s on %s", info.AddonName, clusterName),
					Description: fmt.Sprintf("Running %s, catalog has %s", clusterVersion, info.CatalogVersion),
					Timestamp:   now,
				})
			}
		}
	}
}

// isMajorOrMinorUpgrade returns true when the latest version differs from
// current in the major or minor component (patch-only bumps are ignored).
func isMajorOrMinorUpgrade(current, latest string) bool {
	curParts := parseSemverParts(current)
	latParts := parseSemverParts(latest)
	if len(curParts) < 2 || len(latParts) < 2 {
		return current != latest
	}
	return curParts[0] != latParts[0] || curParts[1] != latParts[1]
}

// parseSemverParts parses a semver string into its numeric components.
// It strips a leading "v" and any pre-release suffix (e.g. "-beta.1").
func parseSemverParts(v string) []int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	result := make([]int, 0, 3)
	for _, p := range parts {
		p = strings.Split(p, "-")[0] // strip pre-release
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		result = append(result, n)
	}
	return result
}
