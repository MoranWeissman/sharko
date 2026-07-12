/**
 * DoctorModal — connection doctor (V2-cleanup-88.4 / 88.5, warn status
 * added by V2-cleanup-90.1). Mirrors DiagnoseModal's shape (fetch-on-open,
 * loading/error/result states) but renders the doctor's real-attempt
 * checks instead of IAM policy simulation: connection credentials, addon
 * secret paths, cross-account role assumption, cluster access, and secret
 * ownership. Each check is pass / warn / fail / not-applicable, with a
 * plain-English fix highlighted on warn or fail.
 */
import { useState, useEffect } from 'react';
import { CheckCircle, XCircle, MinusCircle, AlertTriangle, Loader2, Stethoscope } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog';
import { doctorCluster } from '@/services/api';
import type { DoctorClusterResponse, DoctorCheck } from '@/services/models';

interface DoctorModalProps {
  clusterName: string;
  open: boolean;
  onClose: () => void;
}

export const DOCTOR_LABEL = 'Diagnose connection';
export const DOCTOR_HINT = 'Checks connection credentials, addon secret paths, cross-account role (if configured), cluster access, and secret ownership. Tells you exactly what to fix on failure.';

const CHECK_LABELS: Record<DoctorCheck['id'], string> = {
  'connection-credentials': 'Connection credentials',
  'addon-secret-paths': 'Addon secret paths',
  'assume-role': 'Cross-account role',
  'cluster-access': 'Cluster access',
  'secret-ownership': 'Secret ownership',
};

function CheckIcon({ status }: { status: DoctorCheck['status'] }) {
  if (status === 'pass') {
    return <CheckCircle className="h-4 w-4 shrink-0 text-green-600 dark:text-green-400" aria-label="Passed" />;
  }
  if (status === 'warn') {
    return <AlertTriangle className="h-4 w-4 shrink-0 text-amber-500 dark:text-amber-400" aria-label="Warning" />;
  }
  if (status === 'fail') {
    return <XCircle className="h-4 w-4 shrink-0 text-red-500 dark:text-red-400" aria-label="Failed" />;
  }
  return <MinusCircle className="h-4 w-4 shrink-0 text-[#5a8aaa] dark:text-gray-500" aria-label="Not applicable" />;
}

export function DoctorModal({ clusterName, open, onClose }: DoctorModalProps) {
  const [result, setResult] = useState<DoctorClusterResponse | null>(null);
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
    doctorCluster(clusterName)
      .then(setResult)
      .catch((e) => setError(e instanceof Error ? e.message : 'Connection doctor failed'))
      .finally(() => setLoading(false));
  }, [open, clusterName]);

  const hasFail = result?.checks.some((c) => c.status === 'fail') ?? false;

  const overallCopy =
    result?.overall === 'pass'
      ? 'All checks passed — this cluster’s connection looks healthy.'
      : result?.overall === 'partial'
        ? hasFail
          ? 'Some checks passed and some failed — see the fixes below.'
          : 'Everything checked out, but one or more checks raised a warning — see the details below.'
        : 'Every check that ran failed — see the fixes below.';
  const overallClasses =
    result?.overall === 'pass'
      ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-400'
      : result?.overall === 'partial'
        ? 'bg-amber-50 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300'
        : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-400';

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) onClose(); }}>
      <DialogContent className="max-w-2xl max-h-[80vh] overflow-y-auto" data-testid="doctor-modal">
        <DialogHeader>
          <DialogTitle>{DOCTOR_LABEL}: {clusterName}</DialogTitle>
          <DialogDescription>{DOCTOR_HINT}</DialogDescription>
        </DialogHeader>

        {loading && (
          <div className="flex items-center justify-center py-12">
            <Loader2 className="h-6 w-6 animate-spin text-[#2a5a7a] dark:text-gray-400" />
            <span className="ml-2 text-sm text-[#2a5a7a] dark:text-gray-400">Running the doctor…</span>
          </div>
        )}

        {error && (
          <div className="rounded-md bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400">
            {error}
          </div>
        )}

        {result && !loading && (
          <div className="space-y-4">
            <div className={`flex items-center gap-2 rounded-md px-4 py-3 text-sm font-medium ${overallClasses}`}>
              <Stethoscope className="h-5 w-5 shrink-0" />
              {overallCopy}
            </div>

            <div className="space-y-2" data-testid="doctor-checks">
              {result.checks.map((check) => (
                <div
                  key={check.id}
                  data-testid={`doctor-check-${check.id}`}
                  data-status={check.status}
                  className={`rounded-md px-3 py-2.5 text-sm ${
                    check.status === 'fail'
                      ? 'bg-red-50 dark:bg-red-900/10'
                      : check.status === 'warn'
                        ? 'bg-amber-50 dark:bg-amber-900/10'
                        : check.status === 'pass'
                          ? 'bg-green-50 dark:bg-green-900/10'
                          : 'bg-[#f0f7ff] dark:bg-gray-800'
                  }`}
                >
                  <div className="flex items-center gap-2">
                    <CheckIcon status={check.status} />
                    <span className="font-medium text-[#0a2a4a] dark:text-gray-200">
                      {CHECK_LABELS[check.id] ?? check.id}
                    </span>
                  </div>
                  <p className="ml-6 mt-0.5 text-xs text-[#3a6a8a] dark:text-gray-400">{check.detail}</p>
                  {(check.status === 'fail' || check.status === 'warn') && check.fix && (
                    <p
                      data-testid={`doctor-fix-${check.id}`}
                      className="ml-6 mt-1.5 rounded-md bg-amber-50 px-2.5 py-1.5 text-xs font-medium text-amber-800 ring-1 ring-amber-200 dark:bg-amber-900/20 dark:text-amber-300 dark:ring-amber-800"
                    >
                      Fix: {check.fix}
                    </p>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
