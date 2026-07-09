/**
 * ClusterIdentityStrip — the identity summary at the top of the Register
 * New Cluster dialog, shrunk to one calm line (V2-cleanup-89.2).
 *
 * V2-cleanup-88.5 put the full identity explainer (ARN, method badge, and
 * an expandable "How identity-based access works" panel) inside the Register
 * dialog. Maintainer feedback: it's read-only information dominating a form
 * — a newcomer thinks they must act on it. The full explainer now lives on
 * the System page (`ClusterIdentityPanel`, rendered by `SystemView`); this
 * strip is the one line that stays in the dialog, pointing there for detail.
 *
 * V2-cleanup-89.8: dropped the "Layer 1" header entirely — it was internal
 * design vocabulary that gave users nothing. A one-line strip needs no
 * section header.
 *
 * Purely presentational: the parent (ClustersOverview) still owns the fetch
 * and passes the result down, same as before.
 */
import { Link } from 'react-router-dom';
import { CheckCircle2, AlertTriangle, Loader2 } from 'lucide-react';
import type { SystemCapabilitiesResponse } from '@/services/models';
import { IDENTITY_DOCS_URL } from '@/components/ClusterIdentityPanel';

interface ClusterIdentityStripProps {
  capabilities: SystemCapabilitiesResponse | null;
  loading: boolean;
}

export function ClusterIdentityStrip({ capabilities, loading }: ClusterIdentityStripProps) {
  const detected = !loading && capabilities?.aws.detected === true;
  const notDetected = !loading && capabilities !== null && capabilities.aws.detected === false;
  // Same "treat unknown as not-detected" rule as before (V2-cleanup-88.5):
  // truthful either way, never blocks the form.
  const showNotDetected = notDetected || (!loading && capabilities === null);

  return (
    <div
      data-testid="identity-strip"
      className="rounded-lg ring-2 ring-[#6aade0] bg-[#e8f4ff] p-3 dark:ring-gray-700 dark:bg-gray-900"
    >
      {loading && (
        <p className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
          Checking Sharko's own identity…
        </p>
      )}

      {detected && (
        <p
          className="flex flex-wrap items-center gap-1.5 text-sm font-medium text-green-700 dark:text-green-400"
          data-testid="identity-strip-detected"
        >
          <CheckCircle2 className="h-4 w-4 shrink-0" />
          Sharko has an AWS identity — EKS clusters that trust it need no stored credentials (details in{' '}
          <Link
            to="/system"
            className="font-medium text-teal-700 underline hover:text-teal-800 dark:text-teal-400 dark:hover:text-teal-300"
          >
            System
          </Link>
          ).
        </p>
      )}

      {showNotDetected && (
        <p
          className="flex flex-wrap items-center gap-1.5 text-sm text-amber-700 dark:text-amber-400"
          data-testid="identity-strip-not-detected"
        >
          <AlertTriangle className="h-4 w-4 shrink-0" />
          No AWS identity detected — for EKS clusters, paste a kubeconfig or point at a secret.{' '}
          <a
            href={IDENTITY_DOCS_URL}
            target="_blank"
            rel="noopener noreferrer"
            className="font-medium text-teal-700 underline hover:text-teal-800 dark:text-teal-400 dark:hover:text-teal-300"
          >
            See the setup guide
          </a>
          .
        </p>
      )}
    </div>
  );
}
