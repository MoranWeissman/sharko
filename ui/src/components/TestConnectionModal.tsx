import { useState, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { Loader2, KeyRound } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog';
import { testClusterConnection, isTestClusterUnavailable } from '@/services/api';
import type { TestClusterUnavailable } from '@/services/api';
import type { VerifyStep } from '@/services/models';
import { TEST_CONNECTION_LABEL, TEST_CONNECTION_HINT } from '@/components/ClusterActionHints';
import { SHARKO_CONN_LABEL, SHARKO_CONN_TOOLTIP } from '@/components/WhoseConnectionLabel';

// Per-error-code copy + optional action link for the Test-unavailable
// banner. Production targets are self-hosted K8s + AWS-managed clusters;
// kind/minikube are dev-only and must not anchor production-facing copy.
// The aws-iam-cluster-auth docs link points at an in-app placeholder
// today; it will resolve once the operator-docs page lands.
// (Moved into the modal by V2-cleanup-91.1/F5 — the Test-connection result,
// including this typed guidance, now lives in TestConnectionModal.)
function TestUnavailableBanner({ result }: { result: TestClusterUnavailable }) {
  let title: string;
  let body: string;
  let actionTo: string | null = null;
  let actionLabel: string | null = null;

  switch (result.error_code) {
    case 'no_secrets_backend':
      title = 'Cluster test unavailable';
      body = result.error;
      actionTo = '/settings?section=connections';
      actionLabel = 'Open Settings → Connections';
      break;
    case 'argocd_provider_iam_required':
      title = 'AWS IAM authentication required';
      body =
        "This cluster uses AWS IAM authentication. Configure AWS credentials for the Sharko pod's role (IRSA, EC2 instance profile, or Pod Identity) to enable Test connection for AWS-managed clusters.";
      actionTo = '/docs/operator/aws-iam-cluster-auth';
      actionLabel = 'Open IAM setup guide';
      break;
    case 'argocd_provider_exec_unsupported':
      title = 'Exec-plugin authentication not supported';
      body =
        'This cluster uses exec-plugin auth (e.g. gcloud, azure-cli, aws-iam-authenticator). Exec plugins are not supported in Sharko v1.x — tracked for v2.';
      // No action link — surface the limitation; there is no in-app fix path.
      break;
    case 'argocd_provider_unsupported_auth':
      title = 'Unrecognized cluster authentication';
      body =
        "Unrecognized authentication shape in this cluster's ArgoCD Secret. Inspect the Secret manually in the argocd namespace (kubectl -n argocd get secret <name> -o yaml).";
      // No action link — manual inspection is the only path.
      break;
  }

  return (
    <div
      role="alert"
      data-testid="test-unavailable-banner"
      data-error-code={result.error_code}
      className="rounded-lg ring-2 ring-amber-300 bg-amber-50 px-3 py-2 dark:ring-amber-700 dark:bg-amber-950/30"
    >
      <p className="text-xs font-semibold text-amber-800 dark:text-amber-300">{title}</p>
      <p className="mt-0.5 text-xs text-amber-700 dark:text-amber-300">{body}</p>
      {actionTo && actionLabel && (
        <Link
          to={actionTo}
          className="mt-1 inline-block text-xs font-medium text-amber-800 underline hover:text-amber-900 dark:text-amber-300 dark:hover:text-amber-200"
        >
          {actionLabel}
        </Link>
      )}
    </div>
  );
}

type TestResult =
  | { reachable?: boolean; success?: boolean; server_version?: string; error?: string; error_message?: string; suggestions?: string[]; steps?: VerifyStep[] }
  | TestClusterUnavailable;

interface TestConnectionModalProps {
  clusterName: string;
  open: boolean;
  onClose: () => void;
  onSuggestionSelect: (suggestion: string) => void;
}

export function TestConnectionModal({ clusterName, open, onClose, onSuggestionSelect }: TestConnectionModalProps) {
  const [result, setResult] = useState<TestResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      setResult(null);
      setError(null);
      return;
    }
    setLoading(true);
    setError(null);
    testClusterConnection(clusterName)
      .then(setResult)
      .catch((e) => setError(e instanceof Error ? e.message : 'Test failed'))
      .finally(() => setLoading(false));
  }, [open, clusterName]);

  const handleSuggestionClick = (suggestion: string) => {
    onSuggestionSelect(suggestion);
    onClose();
  };

  // The Test endpoint can be "unavailable" for several typed reasons — a
  // missing secrets backend, AWS IAM auth, etc. Those aren't a plain
  // Connected/Unreachable result; they get their own guidance banner with a
  // fix link. isTestClusterUnavailable is the same discriminator the API
  // ships, kept in sync with the contract.
  const unavailable = result !== null && isTestClusterUnavailable(result);
  const testResult = result !== null && !isTestClusterUnavailable(result) ? result : null;

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) onClose(); }}>
      <DialogContent className="max-w-2xl max-h-[80vh] overflow-y-auto" data-testid="test-connection-modal">
        <DialogHeader>
          <DialogTitle>{TEST_CONNECTION_LABEL}: {clusterName}</DialogTitle>
          <DialogDescription>{TEST_CONNECTION_HINT}</DialogDescription>
        </DialogHeader>

        {loading && (
          <div className="flex items-center justify-center py-12">
            <Loader2 className="h-6 w-6 animate-spin text-[#2a5a7a] dark:text-gray-400" />
            <span className="ml-2 text-sm text-[#2a5a7a] dark:text-gray-400">Testing connection...</span>
          </div>
        )}

        {error && !loading && (
          <div className="rounded-md bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400">
            {error}
          </div>
        )}

        {unavailable && result && !loading && (
          <TestUnavailableBanner result={result as TestClusterUnavailable} />
        )}

        {testResult && !loading && (
          <div className="space-y-4">
            {/* Step-by-step test results */}
            {testResult.steps && testResult.steps.length > 0 && (
              <div className="rounded-lg bg-[#f8fbff] p-3 ring-1 ring-[#d0e4f5] dark:bg-gray-800 dark:ring-gray-700">
                <p className="mb-2 cursor-help text-xs font-semibold text-[#0a2a4a] dark:text-gray-200" title={SHARKO_CONN_TOOLTIP}>
                  Connection test results ({SHARKO_CONN_LABEL}):
                </p>
                <div className="space-y-1">
                  {testResult.steps.map((step, i) => (
                    <div key={i} className="flex items-center gap-2 text-xs">
                      {step.status === 'pass' && (
                        <span className="text-green-600 dark:text-green-400">&#10003;</span>
                      )}
                      {step.status === 'fail' && (
                        <span className="text-red-600 dark:text-red-400">&#10007;</span>
                      )}
                      {step.status === 'skipped' && (
                        <span className="text-[#5a8aaa] dark:text-gray-500">&#9675;</span>
                      )}
                      <span className={
                        step.status === 'pass'
                          ? 'text-[#0a2a4a] dark:text-gray-200'
                          : step.status === 'fail'
                            ? 'text-red-700 dark:text-red-400'
                            : 'text-[#5a8aaa] dark:text-gray-500'
                      }>
                        {step.name}
                        {step.detail && step.status !== 'skipped' && (
                          <span className="ml-1 text-[#3a6a8a] dark:text-gray-400">
                            {step.status === 'fail' ? ` — ${step.detail}` : ` (${step.detail})`}
                          </span>
                        )}
                        {step.status === 'skipped' && (
                          <span className="ml-1 text-[#5a8aaa] dark:text-gray-500">(skipped)</span>
                        )}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Summary badge — a single text node so the whole
              * "Connected — v1.29.3" line reads as one string. */}
            <div
              title={SHARKO_CONN_TOOLTIP}
              className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-xs font-medium ${
                testResult.reachable || testResult.success
                  ? 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400'
                  : 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
              }`}
            >
              {testResult.reachable || testResult.success
                ? `Connected${testResult.server_version ? ` — ${testResult.server_version}` : ''}`
                : testResult.error || testResult.error_message || 'Unreachable'}
            </div>

            {/* Suggestions panel */}
            {!testResult.reachable && !testResult.success && testResult.suggestions && testResult.suggestions.length > 0 && (
              <div className="rounded-lg bg-[#e8f4ff] p-3 ring-2 ring-[#6aade0] dark:bg-gray-800 dark:ring-gray-700">
                <p className="text-xs font-semibold text-[#0a2a4a] dark:text-gray-200">Similar secrets found:</p>
                <div className="mt-1.5 flex flex-wrap gap-1.5">
                  {testResult.suggestions.map((s) => (
                    <button
                      key={s}
                      onClick={() => handleSuggestionClick(s)}
                      className="inline-flex items-center gap-1 rounded-md bg-[#f0f7ff] px-2.5 py-1 text-xs font-medium text-[#0a3a5a] ring-1 ring-[#5a9dd0] hover:bg-[#d6eeff] dark:bg-gray-700 dark:text-gray-200 dark:ring-gray-600 dark:hover:bg-gray-600"
                    >
                      <KeyRound className="h-3 w-3" />
                      {s}
                    </button>
                  ))}
                </div>
              </div>
            )}
            {!testResult.reachable && !testResult.success && (!testResult.suggestions || testResult.suggestions.length === 0) && (
              <p className="text-xs text-[#3a6a8a] dark:text-gray-400">
                Set the secret path manually in cluster settings.
              </p>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
