import type { ReactElement, ReactNode } from 'react';

function formatInlineMarkdown(text: string): ReactNode {
  const parts: ReactNode[] = [];
  let remaining = text;
  let keyIdx = 0;

  while (remaining.length > 0) {
    // Find first match among bold, inline code, and links
    const boldMatch = remaining.match(/\*\*(.+?)\*\*/);
    const codeMatch = remaining.match(/`([^`]+)`/);
    const linkMatch = remaining.match(/\[([^\]]+)\]\(([^)]+)\)/);

    const boldIdx = boldMatch?.index ?? Infinity;
    const codeIdx = codeMatch?.index ?? Infinity;
    const linkIdx = linkMatch?.index ?? Infinity;

    const minIdx = Math.min(boldIdx, codeIdx, linkIdx);
    if (minIdx === Infinity) {
      parts.push(remaining);
      break;
    }

    if (boldIdx === minIdx && boldMatch) {
      parts.push(remaining.slice(0, boldIdx));
      parts.push(<strong key={keyIdx++}>{boldMatch[1]}</strong>);
      remaining = remaining.slice(boldIdx + boldMatch[0].length);
    } else if (codeIdx === minIdx && codeMatch) {
      parts.push(remaining.slice(0, codeIdx));
      parts.push(
        <code
          key={keyIdx++}
          className="rounded bg-gray-200 px-1 py-0.5 text-xs dark:bg-gray-700"
        >
          {codeMatch[1]}
        </code>,
      );
      remaining = remaining.slice(codeIdx + codeMatch[0].length);
    } else if (linkMatch) {
      parts.push(remaining.slice(0, linkIdx));
      parts.push(
        <a
          key={keyIdx++}
          href={linkMatch[2]}
          target="_blank"
          rel="noopener noreferrer"
          className="text-cyan-600 underline hover:text-cyan-700 dark:text-cyan-400"
        >
          {linkMatch[1]}
        </a>,
      );
      remaining = remaining.slice(linkIdx + linkMatch[0].length);
    }
  }

  return parts.length === 1 ? parts[0] : <>{parts}</>;
}

export function MarkdownRenderer({ content }: { content: string }) {
  const lines = content.split('\n');
  const elements: ReactElement[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];
    const trimmed = line.trim();

    // Fenced code block
    if (trimmed.startsWith('```')) {
      const codeLines: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trim().startsWith('```')) {
        codeLines.push(lines[i]);
        i++;
      }
      i++; // skip closing ```
      elements.push(
        <pre
          key={elements.length}
          className="my-2 overflow-x-auto rounded-lg bg-gray-900 p-3 text-xs leading-relaxed text-gray-300"
        >
          <code>{codeLines.join('\n')}</code>
        </pre>,
      );
      continue;
    }

    // Empty line
    if (trimmed === '') {
      elements.push(<div key={elements.length} className="h-3" />);
      i++;
      continue;
    }

    // Headers
    if (trimmed.startsWith('### ')) {
      elements.push(
        <h4
          key={elements.length}
          className="mt-4 mb-1.5 text-sm font-bold text-gray-800 dark:text-gray-200"
        >
          {formatInlineMarkdown(trimmed.slice(4))}
        </h4>,
      );
      i++;
      continue;
    }
    if (trimmed.startsWith('## ')) {
      elements.push(
        <h3
          key={elements.length}
          className="mt-5 mb-2 border-b border-gray-200 pb-1.5 text-base font-bold text-gray-900 dark:border-gray-700 dark:text-white"
        >
          {formatInlineMarkdown(trimmed.slice(3))}
        </h3>,
      );
      i++;
      continue;
    }
    if (trimmed.startsWith('# ')) {
      elements.push(
        <h3
          key={elements.length}
          className="mt-4 text-lg font-bold text-gray-900 dark:text-white"
        >
          {formatInlineMarkdown(trimmed.slice(2))}
        </h3>,
      );
      i++;
      continue;
    }

    // Numbered list item
    if (/^\d+\.\s/.test(trimmed)) {
      elements.push(
        <div key={elements.length} className="ml-4 mt-1.5 flex gap-2.5">
          <span className="shrink-0 font-bold text-cyan-600 dark:text-cyan-400">
            {trimmed.match(/^\d+/)?.[0]}.
          </span>
          <span className="flex-1">
            {formatInlineMarkdown(trimmed.replace(/^\d+\.\s*/, ''))}
          </span>
        </div>,
      );
      i++;
      continue;
    }

    // Bullet point
    if (trimmed.startsWith('- ') || trimmed.startsWith('* ')) {
      elements.push(
        <div key={elements.length} className="ml-4 mt-1.5 flex gap-2.5">
          <span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-cyan-500" />
          <span className="flex-1">
            {formatInlineMarkdown(trimmed.slice(2))}
          </span>
        </div>,
      );
      i++;
      continue;
    }

    // Regular paragraph
    elements.push(
      <p key={elements.length} className="mt-1.5 text-sm text-gray-700 dark:text-gray-300">
        {formatInlineMarkdown(trimmed)}
      </p>,
    );
    i++;
  }

  return <div className="space-y-0.5">{elements}</div>;
}
