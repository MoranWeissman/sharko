import { CheckCircle, XCircle, HelpCircle, AlertTriangle } from 'lucide-react';

interface ConnectionStatusProps {
  status: string;
}

function getConnectionInfo(status: string): {
  icon: React.ElementType;
  label: string;
  colorClass: string;
} {
  const s = status.toLowerCase();

  if (s === 'connected' || s === 'successful') {
    return {
      icon: CheckCircle,
      label: 'Connected',
      colorClass: 'text-green-600',
    };
  }
  if (s === 'failed') {
    return { icon: XCircle, label: 'Failed', colorClass: 'text-red-600' };
  }
  if (s === 'missing' || s === 'missing_from_argocd') {
    return {
      icon: HelpCircle,
      label: 'Missing from ArgoCD',
      colorClass: 'text-yellow-600',
    };
  }
  if (s === 'not_in_git') {
    return {
      icon: AlertTriangle,
      label: 'Not in Git',
      colorClass: 'text-purple-600',
    };
  }

  return { icon: HelpCircle, label: 'Unknown', colorClass: 'text-gray-500' };
}

export function ConnectionStatus({ status }: ConnectionStatusProps) {
  const { icon: Icon, label, colorClass } = getConnectionInfo(status);

  return (
    <span className={`inline-flex items-center gap-1.5 ${colorClass}`}>
      <Icon className="h-4 w-4" />
      <span className="text-sm font-medium">{label}</span>
    </span>
  );
}
