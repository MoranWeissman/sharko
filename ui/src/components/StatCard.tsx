import type { ReactNode } from 'react';

interface StatCardProps {
  title: string;
  value: string | number;
  icon?: ReactNode;
  color?: 'default' | 'success' | 'error' | 'warning';
  onClick?: () => void;
  selected?: boolean;
  subtitle?: string;
}

const borderColorMap: Record<string, string> = {
  default: 'border-l-gray-300 dark:border-l-gray-600',
  success: 'border-l-green-500',
  error: 'border-l-red-500',
  warning: 'border-l-yellow-500',
};

export function StatCard({
  title,
  value,
  icon,
  color = 'default',
  onClick,
  selected = false,
  subtitle,
}: StatCardProps) {
  const borderClass = borderColorMap[color];
  const isClickable = Boolean(onClick);

  const selectedClass = selected
    ? 'ring-2 ring-cyan-500 ring-offset-1 shadow-md dark:ring-offset-gray-900'
    : '';

  const interactiveClass = isClickable
    ? 'cursor-pointer transition-shadow hover:shadow-md'
    : '';

  return (
    <div
      role={isClickable ? 'button' : undefined}
      tabIndex={isClickable ? 0 : undefined}
      onClick={onClick}
      onKeyDown={
        isClickable
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                onClick?.();
              }
            }
          : undefined
      }
      className={`relative rounded-lg border border-l-4 bg-white p-4 shadow-sm dark:border-gray-700 dark:bg-gray-800 ${borderClass} ${selectedClass} ${interactiveClass}`}
    >
      {icon && (
        <div className="absolute right-4 top-4 text-gray-400 dark:text-gray-500">{icon}</div>
      )}
      <div className="text-2xl font-bold text-gray-900 dark:text-gray-100">{value}</div>
      <div className="mt-1 text-sm text-gray-500 dark:text-gray-400">{title}</div>
      {subtitle && (
        <div className="mt-0.5 text-xs text-gray-400 dark:text-gray-500">{subtitle}</div>
      )}
    </div>
  );
}
