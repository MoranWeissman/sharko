import * as React from 'react';
import { cn } from '@/lib/utils';

/**
 * HelperText — readable helper text component (V3 U1).
 *
 * Mirrors shadcn's DialogDescription styling (`text-sm text-muted-foreground`)
 * for consistent, AA-contrast load-bearing helper text across the app.
 *
 * Use this for helper text that conveys important information (diagnostics hints,
 * field explanations, etc.). Leave genuinely-secondary uses (timestamps, captions,
 * placeholders) with their existing faint styles.
 */
export function HelperText({
  className,
  ...props
}: React.HTMLAttributes<HTMLParagraphElement>) {
  return (
    <p
      className={cn('text-sm text-muted-foreground', className)}
      {...props}
    />
  );
}
