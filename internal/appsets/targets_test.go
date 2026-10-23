package appsets

import "testing"

func connectivityCheckAppSet() map[string]interface{} {
	return map[string]interface{}{
		"metadata": map[string]interface{}{"name": "sharko-connectivity-check", "namespace": "argocd"},
		"spec": map[string]interface{}{
			"generators": []interface{}{
				map[string]interface{}{"clusters": map[string]interface{}{
					"selector": map[string]interface{}{"matchLabels": map[string]interface{}{
						"argocd.argoproj.io/secret-type": "cluster",
						"sharko.dev/connectivity-check":  "enabled",
					}},
				}},
			},
		},
	}
}

func TestTargetsCluster_OnlyWhenSelectorMatches(t *testing.T) {
	as := Parse(connectivityCheckAppSet())
	legacy := map[string]string{"argocd.argoproj.io/secret-type": "cluster", "env": "dev", "team": "a"}
	if as.TargetsCluster(legacy) {
		t.Fatal("the connectivity-check ApplicationSet does not pick a cluster without its label")
	}
	legacy["sharko.dev/connectivity-check"] = "enabled"
	if !as.TargetsCluster(legacy) {
		t.Fatal("it does pick a cluster that has the label")
	}
}

func TestTargetsCluster_MatchExpressionsAndNested(t *testing.T) {
	obj := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "nested"},
		"spec": map[string]interface{}{"generators": []interface{}{
			map[string]interface{}{"matrix": map[string]interface{}{"generators": []interface{}{
				map[string]interface{}{"git": map[string]interface{}{}},
				map[string]interface{}{"clusters": map[string]interface{}{
					"selector": map[string]interface{}{"matchExpressions": []interface{}{
						map[string]interface{}{"key": "env", "operator": "In", "values": []interface{}{"prod"}},
					}},
				}},
			}}},
		}},
	}
	as := Parse(obj)
	if as.TargetsCluster(map[string]string{"env": "dev"}) {
		t.Fatal("env=dev does not match env In (prod)")
	}
	if !as.TargetsCluster(map[string]string{"env": "prod"}) {
		t.Fatal("env=prod matches env In (prod)")
	}
}

func TestTargetsCluster_NoSelectorPicksEveryCluster(t *testing.T) {
	obj := map[string]interface{}{"spec": map[string]interface{}{"generators": []interface{}{
		map[string]interface{}{"clusters": map[string]interface{}{}},
	}}}
	if !Parse(obj).TargetsCluster(nil) {
		t.Fatal("a cluster generator with no selector picks every cluster")
	}
}

func TestTargetsCluster_NoClusterGeneratorDoesNotTarget(t *testing.T) {
	obj := map[string]interface{}{"spec": map[string]interface{}{"generators": []interface{}{
		map[string]interface{}{"list": map[string]interface{}{}},
	}}}
	if Parse(obj).TargetsCluster(map[string]string{"env": "dev"}) {
		t.Fatal("an ApplicationSet without a cluster generator does not pick clusters by label")
	}
}

func TestTargetsCluster_UnknownCountsAsMatch(t *testing.T) {
	// Not from Parse: Sharko knows nothing about its selectors.
	if !(ApplicationSetInfo{Name: "x"}).TargetsCluster(nil) {
		t.Fatal("an ApplicationSet Sharko could not read counts as picking the cluster")
	}
	bad := map[string]interface{}{"spec": map[string]interface{}{"generators": []interface{}{
		map[string]interface{}{"clusters": map[string]interface{}{
			"selector": map[string]interface{}{"matchExpressions": []interface{}{
				map[string]interface{}{"key": "env", "operator": "NotAnOperator"},
			}},
		}},
	}}}
	if !Parse(bad).TargetsCluster(map[string]string{"env": "dev"}) {
		t.Fatal("a selector Sharko cannot understand counts as picking the cluster")
	}
}
