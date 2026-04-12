package diagnose

import (
	"fmt"
	"strings"
)

// generateFix returns a copy-paste-ready K8s RBAC fix for a failed permission check.
func generateFix(permission, namespace, callerARN string) Fix {
	switch {
	case strings.HasPrefix(permission, "list namespaces"):
		return Fix{
			Description: fmt.Sprintf("Grant cluster-wide namespace list permission for %s", callerARN),
			YAML: fmt.Sprintf(`# K8s RBAC - apply to target cluster
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: sharko-namespace-list
rules:
- apiGroups: [""]
  resources: ["namespaces"]
  verbs: ["list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: sharko-namespace-list
subjects:
- kind: Group
  name: sharko-access
  apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: sharko-namespace-list
  apiGroup: rbac.authorization.k8s.io`, ),
		}

	case strings.HasPrefix(permission, "get namespace"):
		return Fix{
			Description: fmt.Sprintf("Grant namespace get permission for %s in %s", callerARN, namespace),
			YAML: fmt.Sprintf(`# K8s RBAC - apply to target cluster
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: sharko-namespace-get
rules:
- apiGroups: [""]
  resources: ["namespaces"]
  verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: sharko-namespace-get
subjects:
- kind: Group
  name: sharko-access
  apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: sharko-namespace-get
  apiGroup: rbac.authorization.k8s.io`, ),
		}

	case strings.HasPrefix(permission, "create secret"),
		strings.HasPrefix(permission, "get secret"),
		strings.HasPrefix(permission, "delete secret"):
		return Fix{
			Description: fmt.Sprintf("Grant secret CRUD permissions for %s in namespace %s", callerARN, namespace),
			YAML: fmt.Sprintf(`# K8s RBAC - apply to target cluster
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: sharko-addon-delivery
  namespace: %s
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["create", "get", "update", "delete", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: sharko-addon-delivery
  namespace: %s
subjects:
- kind: Group
  name: sharko-access
  apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: Role
  name: sharko-addon-delivery
  apiGroup: rbac.authorization.k8s.io`, namespace, namespace),
		}

	default:
		return Fix{
			Description: fmt.Sprintf("Permission check failed: %s — review RBAC for %s", permission, callerARN),
			YAML:        "# No auto-generated fix available for this permission",
		}
	}
}
