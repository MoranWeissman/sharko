/**
 * ClusterIdentityPanel — the full identity explainer: detected AWS identity
 * ARN, detection method, and an expandable "How identity-based access
 * works" panel.
 *
 * Originally Layer 1 of the two-layer registration dialog (V2-cleanup-88.5).
 * The maintainer's complaint about that placement: it's read-only
 * information dominating a form — a newcomer registering a cluster thinks
 * they must act on it. V2-cleanup-89.2 moved this full panel to the System
 * page (the "whole chain" read-only screen); the dialog keeps only a
 * one-line summary (`ClusterIdentityStrip`) that links here.
 *
 * Purely presentational: the caller (SystemView) owns the fetch and passes
 * the result down, the same pattern already used for catalogAddons.
 */
import { useState } from 'react';
import { ShieldCheck, Info, ChevronDown, ChevronUp, Loader2 } from 'lucide-react';
import type { SystemCapabilitiesResponse } from '@/services/models';

// The hub-and-spoke identity recipe (V2-cleanup-88.6, #511) — same
// readthedocs pattern as the values-editing docs link on the Config tab
// (see PerClusterAddonOverridesEditor.tsx).
export const IDENTITY_DOCS_URL =
  'https://sharko.readthedocs.io/en/latest/operator/eks-hub-and-spoke-identity/';

interface ClusterIdentityPanelProps {
  capabilities: SystemCapabilitiesResponse | null;
  loading: boolean;
}

export function ClusterIdentityPanel({ capabilities, loading }: ClusterIdentityPanelProps) {
  const [howOpen, setHowOpen] = useState(false);
  const detected = !loading && capabilities?.aws.detected === true;
  const notDetected = !loading && capabilities !== null && capabilities.aws.detected === false;
  // Treat "capabilities never loaded" (mock without the endpoint, or a
  // failed fetch) the same as "not detected" — the not-detected copy is
  // truthful either way (Sharko cannot currently prove it has an AWS
  // identity) and never blocks the form.
  const showNotDetected = notDetected || (!loading && capabilities === null);

  return (
    <div
      data-testid="identity-panel"
      className="rounded-lg ring-2 ring-[#6aade0] bg-[#e8f4ff] p-3 dark:ring-gray-700 dark:bg-gray-900"
    >
      <p className="text-xs font-semibold uppercase tracking-wide text-[#3a6a8a] dark:text-gray-400">
        Detected once per hub — applies to every cluster
      </p>

      {loading && (
        <p className="mt-1 flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
          Checking Sharko's own identity…
        </p>
      )}

      {detected && capabilities && (
        <div className="mt-1 space-y-1" data-testid="identity-detected">
          <p className="flex flex-wrap items-center gap-1.5 text-sm font-medium text-green-700 dark:text-green-400">
            <ShieldCheck className="h-4 w-4 shrink-0" />
            Sharko is running with an AWS identity:{' '}
            <code className="rounded bg-white/60 px-1 font-mono text-xs text-[#0a2a4a] dark:bg-gray-800 dark:text-gray-200">
              {capabilities.aws.identity_arn}
            </code>{' '}
            ({capabilities.aws.method})
          </p>
          <p className="text-xs text-[#2a5a7a] dark:text-gray-400">
            EKS clusters that trust this identity need no stored credentials.
          </p>
        </div>
      )}

      {showNotDetected && (
        <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-300" data-testid="identity-not-detected">
          No AWS identity detected — for EKS clusters, paste a kubeconfig or point at a secret. To
          enable identity-based access,{' '}
          <a
            href={IDENTITY_DOCS_URL}
            target="_blank"
            rel="noopener noreferrer"
            className="font-medium text-teal-600 underline hover:text-teal-700 dark:text-teal-400 dark:hover:text-teal-300"
          >
            see the setup guide
          </a>
          .
        </p>
      )}

      <button
        type="button"
        onClick={() => setHowOpen((o) => !o)}
        aria-expanded={howOpen}
        className="mt-2 inline-flex items-center gap-1 text-xs text-[#3a6a8a] hover:text-[#0a3a5a] dark:text-gray-400 dark:hover:text-gray-200"
      >
        <Info className="h-3.5 w-3.5" />
        How identity-based access works
        {howOpen ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
      </button>
      {howOpen && (
        <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400" data-testid="identity-how-it-works">
          Sharko runs with one IAM role on the hub cluster. Every spoke cluster that opts in trusts
          that one role — nothing per-cluster to set up on Sharko's side. When Sharko needs to
          reach a cluster, it generates a short-lived token on demand; nothing is stored.{' '}
          <a
            href={IDENTITY_DOCS_URL}
            target="_blank"
            rel="noopener noreferrer"
            className="font-medium text-teal-600 underline hover:text-teal-700 dark:text-teal-400 dark:hover:text-teal-300"
          >
            Read the full guide
          </a>
          .
        </p>
      )}
    </div>
  );
}
