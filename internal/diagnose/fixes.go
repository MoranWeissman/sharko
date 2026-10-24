package diagnose

import (
	"fmt"
	"strconv"
	"strings"
)

// unknownSubjectName is shown when the cluster would not say which account
// Sharko signs in as. The fix then carries a clear placeholder the user fills
// in, instead of a group name that was made up.
const unknownSubjectName = "REPLACE-WITH-THE-ACCOUNT-SHARKO-SIGNS-IN-AS"

// subjectYAML returns the "subjects" list item of a binding.
func subjectYAML(subject string) string {
	if subject == "" {
		return "- kind: User   # use kind: Group if Sharko signs in through a group\n  name: " + unknownSubjectName + "\n  apiGroup: rbac.authorization.k8s.io"
	}
	// Quoted, so a name with ": " or "#" in it still makes valid YAML
	// (EP3 dry run round R, R12). A Go quoted string is a valid YAML
	// double-quoted string.
	return "- kind: User\n  name: " + strconv.Quote(subject) + "\n  apiGroup: rbac.authorization.k8s.io"
}

// who names the account in a fix description, in plain words.
func who(subject string) string {
	if subject == "" {
		return "the account Sharko signs in as"
	}
	return subject
}

// clusterRoleFix builds a ClusterRole plus its binding.
func clusterRoleFix(name, verbs, subject string) string {
	return fmt.Sprintf(`# K8s RBAC - apply to target cluster
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: %[1]s
rules:
- apiGroups: [""]
  resources: ["namespaces"]
  verbs: [%[2]s]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: %[1]s
subjects:
%[3]s
roleRef:
  kind: ClusterRole
  name: %[1]s
  apiGroup: rbac.authorization.k8s.io`, name, verbs, subjectYAML(subject))
}

// generateFix returns a copy-paste-ready K8s RBAC fix for a failed permission
// check. subject is the account Sharko signs in as on the cluster ("" when the
// cluster would not say).
func generateFix(permission, namespace, subject string) Fix {
	switch {
	case strings.HasPrefix(permission, "list namespaces"):
		return Fix{
			Description: fmt.Sprintf("Let %s list namespaces on this cluster.", who(subject)),
			YAML:        clusterRoleFix("sharko-namespace-list", `"list"`, subject),
		}

	case strings.HasPrefix(permission, "get namespace"):
		return Fix{
			Description: fmt.Sprintf("Let %s read the namespace %s.", who(subject), namespace),
			YAML:        clusterRoleFix("sharko-namespace-get", `"get"`, subject),
		}

	case strings.HasPrefix(permission, "create namespace"),
		strings.HasPrefix(permission, "remove namespace"):
		return Fix{
			Description: fmt.Sprintf("Let %s create and remove the test namespace %s.", who(subject), namespace),
			YAML:        clusterRoleFix("sharko-namespace-manage", `"get", "create", "delete"`, subject),
		}

	case strings.HasPrefix(permission, "create secret"),
		strings.HasPrefix(permission, "get secret"),
		strings.HasPrefix(permission, "delete secret"):
		return Fix{
			Description: fmt.Sprintf("Let %s create, read and delete secrets in the namespace %s.", who(subject), namespace),
			YAML: fmt.Sprintf(`# K8s RBAC - apply to target cluster
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: sharko-addon-delivery
  namespace: %[1]s
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["create", "get", "update", "delete", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: sharko-addon-delivery
  namespace: %[1]s
subjects:
%[2]s
roleRef:
  kind: Role
  name: sharko-addon-delivery
  apiGroup: rbac.authorization.k8s.io`, namespace, subjectYAML(subject)),
		}

	default:
		return Fix{
			Description: fmt.Sprintf("The check %q failed. Look at the rights of %s on this cluster.", permission, who(subject)),
			YAML:        "# No auto-generated fix available for this permission",
		}
	}
}

// addFix adds fix to fixes. A fix with the same YAML as one already there is
// not shown twice, but its reason is kept: it is added to the first one's
// description when it says something different (EP3 dry run round R, R12).
func addFix(fixes []Fix, fix Fix) []Fix {
	for i := range fixes {
		if fixes[i].YAML != fix.YAML {
			continue
		}
		if !strings.Contains(fixes[i].Description, fix.Description) {
			fixes[i].Description += " " + fix.Description
		}
		return fixes
	}
	return append(fixes, fix)
}
