import { useState, useEffect } from 'react';
import { CheckCircle, XCircle, Copy, Check, Loader2, ShieldCheck, ShieldAlert } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog';
import { diagnoseCluster } from '@/services/api';
import type { DiagnosticReport } from '@/services/models';

interface DiagnoseModalProps {
  clusterName: string;
  open: boolean;
  onClose: () => void;
}

export function DiagnoseModal({ clusterName, open, onClose }: DiagnoseModalProps) {
  const [report, setReport] = useState<DiagnosticReport | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copiedIndex, setCopiedIndex] = useState<number | null>(null);

  useEffect(() => {
    if (!open) {
      setReport(null);
      setError(null);
      return;
    }
    setLoading(true);
    setError(null);
    diagnoseCluster(clusterName)
      .then(setReport)
      .catch((e) => setError(e instanceof Error ? e.message : 'Diagnosis failed'))
      .finally(() => setLoading(false));
  }, [open, clusterName]);

  const handleCopy = async (yaml: string, index: number) => {
    try {
      await navigator.clipboard.writeText(yaml);
      setCopiedIndex(index);
      setTimeout(() => setCopiedIndex(null), 2000);
    } catch {
      // Fallback: ignore clipboard errors in non-secure contexts
    }
  };

  const totalChecks = report?.namespace_access?.length ?? 0;
  const passedChecks = report?.namespace_access?.filter((c) => c.passed).length ?? 0;
  const failedChecks = totalChecks - passedChecks;
  const allPassed = totalChecks > 0 && failedChecks === 0;

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) onClose(); }}>
      <DialogContent className="max-w-2xl max-h-[80vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Diagnose: {clusterName}</DialogTitle>
          <DialogDescription>
            Permission checks and suggested fixes for cluster connectivity issues.
          </DialogDescription>
        </DialogHeader>

        {loading && (
          <div className="flex items-center justify-center py-12">
            <Loader2 className="h-6 w-6 animate-spin text-[#2a5a7a] dark:text-gray-400" />
            <span className="ml-2 text-sm text-[#2a5a7a] dark:text-gray-400">Running diagnostics...</span>
          </div>
        )}

        {error && (
          <div className="rounded-md bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400">
            {error}
          </div>
        )}

        {report && !loading && (
          <div className="space-y-5">
            {/* Summary line */}
            {totalChecks > 0 && (
              <div className={`flex items-center gap-2 rounded-md px-4 py-3 text-sm font-medium ${
                allPassed
                  ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-400'
                  : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-400'
              }`}>
                {allPassed ? (
                  <>
                    <ShieldCheck className="h-5 w-5 shrink-0" />
                    All {totalChecks} permission check{totalChecks !== 1 ? 's' : ''} passed — Sharko has the required access
                  </>
                ) : (
                  <>
                    <ShieldAlert className="h-5 w-5 shrink-0" />
                    {failedChecks} of {totalChecks} check{totalChecks !== 1 ? 's' : ''} failed — see fixes below
                  </>
                )}
              </div>
            )}

            {/* Permission Checks */}
            {totalChecks > 0 && (
              <div>
                <h4 className="mb-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-200">Permission Checks</h4>
                <div className="space-y-1.5">
                  {report.namespace_access.map((check, i) => (
                    <div
                      key={i}
                      className={`flex flex-col rounded-md px-3 py-2 text-sm ${
                        check.passed
                          ? 'bg-green-50 dark:bg-green-900/10'
                          : 'bg-red-50 dark:bg-red-900/10'
                      }`}
                    >
                      <div className="flex items-center gap-2">
                        {check.passed ? (
                          <CheckCircle className="h-4 w-4 shrink-0 text-green-600 dark:text-green-400" />
                        ) : (
                          <XCircle className="h-4 w-4 shrink-0 text-red-500 dark:text-red-400" />
                        )}
                        <span className="text-[#0a2a4a] dark:text-gray-200">{check.permission}</span>
                      </div>
                      {check.error && (
                        <p className="ml-6 mt-1 text-xs text-red-600 dark:text-red-400">{check.error}</p>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* All-pass helpful note */}
            {allPassed && (!report.suggested_fixes || report.suggested_fixes.length === 0) && (
              <div className="rounded-md bg-[#f0f7ff] px-4 py-3 text-xs text-[#2a5a7a] dark:bg-gray-800 dark:text-gray-400">
                Sharko can create, read, and delete secrets on this cluster. If the connectivity test still fails,
                the issue may be with credential fetch (AWS Secrets Manager), not cluster permissions.
              </div>
            )}

            {/* Suggested Fixes */}
            {report.suggested_fixes && report.suggested_fixes.length > 0 && (
              <div>
                <h4 className="mb-2 text-sm font-semibold text-red-700 dark:text-red-400">Suggested Fixes</h4>
                <div className="space-y-4">
                  {report.suggested_fixes.map((fix, i) => (
                    <div key={i} className="rounded-md ring-2 ring-red-200 bg-[#fff5f5] p-4 dark:ring-red-800 dark:bg-gray-800">
                      <p className="mb-2 text-sm text-[#0a2a4a] dark:text-gray-200">{fix.description}</p>
                      <div className="relative">
                        <pre className="overflow-x-auto rounded-md bg-[#e8f4ff] p-3 font-mono text-xs text-[#0a2a4a] dark:bg-gray-900 dark:text-gray-300">
                          {fix.yaml}
                        </pre>
                        <button
                          type="button"
                          onClick={() => handleCopy(fix.yaml, i)}
                          className="absolute right-2 top-2 rounded-md bg-[#d6eeff] p-1.5 text-[#2a5a7a] hover:bg-[#bee0ff] dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
                          aria-label="Copy YAML"
                        >
                          {copiedIndex === i ? (
                            <Check className="h-3.5 w-3.5 text-green-600 dark:text-green-400" />
                          ) : (
                            <Copy className="h-3.5 w-3.5" />
                          )}
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
