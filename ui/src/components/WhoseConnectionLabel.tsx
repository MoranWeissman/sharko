// V2-cleanup-55.3: whose-connection attribution.
//
// A cluster's `connection_status` is ArgoCD's OWN connection to the cluster
// (ArgoCD's state from its cluster secret). The Test button is Sharko's OWN
// connection (Sharko fetches the credentials from the secret backend and
// talks to the cluster directly). The two can disagree — ArgoCD can show
// Failed while a Sharko test passes all steps green — so every place one of
// them renders gets a small caption saying whose connection it is.

export const ARGOCD_CONN_LABEL = 'ArgoCD → cluster';
export const ARGOCD_CONN_TOOLTIP =
  "This is ArgoCD's own connection to the cluster. It can fail even when Sharko reaches the cluster fine (Test).";

export const SHARKO_CONN_LABEL = 'Sharko → cluster';
export const SHARKO_CONN_TOOLTIP =
  "This is Sharko's own connection to the cluster: Sharko fetches the credentials from the secret backend and talks to the cluster directly. It can pass even when ArgoCD's own connection is failing.";

interface WhoseConnectionLabelProps {
  who: 'argocd' | 'sharko';
}

/**
 * Small caption rendered above/next to a connection status so the user can
 * tell whose connection it describes. Uses a native `title` tooltip — this
 * renders inside table rows and cards where mounting a Radix tooltip per
 * row would be heavy.
 */
export function WhoseConnectionLabel({ who }: WhoseConnectionLabelProps) {
  const label = who === 'argocd' ? ARGOCD_CONN_LABEL : SHARKO_CONN_LABEL;
  const tooltip = who === 'argocd' ? ARGOCD_CONN_TOOLTIP : SHARKO_CONN_TOOLTIP;
  return (
    <span
      className="w-fit cursor-help text-[10px] font-medium text-[#5a8aaa] dark:text-gray-500"
      title={tooltip}
    >
      {label}
    </span>
  );
}
