/**
 * logger.mjs — JSON-line stderr logger with info/warn/error levels.
 *
 * Stdout stays clean for `--dry-run` JSON capture in CI; humans + CI
 * consumers parse stderr line-by-line. Each line is:
 *
 *   { "ts": "<RFC3339>", "level": "info|warn|error", "msg": "...", ...attrs }
 *
 * `child(attrs)` creates a sub-logger that prefixes every record with
 * the given attributes — used by the runner to scope plugin output:
 *
 *   ctx.logger = log.child({ plugin: 'cncf-landscape' });
 *   ctx.logger.info('starting fetch');
 *   // → {"ts":"...","level":"info","msg":"starting fetch","plugin":"cncf-landscape"}
 */

export function newLogger({ stream = process.stderr, base = {} } = {}) {
  function emit(level, msg, attrs) {
    const rec = { ts: new Date().toISOString(), level, msg, ...base, ...(attrs ?? {}) };
    stream.write(JSON.stringify(rec) + '\n');
  }
  return {
    info: (msg, attrs) => emit('info', msg, attrs),
    warn: (msg, attrs) => emit('warn', msg, attrs),
    error: (msg, attrs) => emit('error', msg, attrs),
    child: (extra) => newLogger({ stream, base: { ...base, ...extra } }),
  };
}
