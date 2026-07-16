package events

// Event Reason constants for Sharko operational events.
// Reasons are stable identifiers in UpperCamelCase format, suitable for switch statements.
//
// Emit events on state CHANGE or genuine FAILURE, not on every reconcile tick.

const (
	// AWS provider failures
	ReasonAWSAssumeRoleFailed   = "AWSAssumeRoleFailed"   // IAM role assumption failed
	ReasonAWSSecretsGetFailed   = "AWSSecretsGetFailed"   // Secrets Manager get failed
	ReasonAWSTokenMintFailed    = "AWSTokenMintFailed"    // EKS token generation (STS) failed
	ReasonAWSConfigLoadFailed   = "AWSConfigLoadFailed"   // AWS SDK config load failed
	ReasonAWSCredentialsInvalid = "AWSCredentialsInvalid" // AWS credentials invalid or expired

	// Host ArgoCD API failures
	ReasonArgoCDUnreachable   = "ArgoCDUnreachable"   // ArgoCD server unreachable (network/DNS)
	ReasonArgoCDAuthFailed    = "ArgoCDAuthFailed"    // ArgoCD auth failed (403/401)
	ReasonArgoCDAPICallFailed = "ArgoCDAPICallFailed" // ArgoCD API call failed (non-auth)

	// Remote cluster connection failures
	ReasonClusterTestFailed       = "ClusterTestFailed"       // Stage1 connectivity test failed
	ReasonClusterDoctorFailed     = "ClusterDoctorFailed"     // Doctor diagnostic failed
	ReasonClusterConnectionFailed = "ClusterConnectionFailed" // General cluster connection failure
	ReasonClusterRBACDenied       = "ClusterRBACDenied"       // RBAC permission denied on remote cluster

	// Git / PR failures
	ReasonPROpenFailed   = "PROpenFailed"   // Failed to open PR via git provider
	ReasonPRMergeFailed  = "PRMergeFailed"  // Failed to merge PR
	ReasonGitPushFailed  = "GitPushFailed"  // Git push failed
	ReasonGitAuthFailed  = "GitAuthFailed"  // Git authentication failed
	ReasonGitCloneFailed = "GitCloneFailed" // Git clone/fetch failed

	// Reconciler failures (placeholder for future EPIC-1 G1/G3 wiring)
	ReasonReconcileFailed = "ReconcileFailed" // Cluster reconciler failed
	ReasonDriftDetected   = "DriftDetected"   // Drift detected between Git and ArgoCD

	// Success events (emit sparingly, only on meaningful milestones)
	ReasonClusterRegistered  = "ClusterRegistered"  // Cluster successfully registered
	ReasonClusterReconciled  = "ClusterReconciled"  // Cluster reconciled successfully
	ReasonPRMerged           = "PRMerged"           // PR merged successfully
	ReasonConnectionRestored = "ConnectionRestored" // Connection to external service restored
)
