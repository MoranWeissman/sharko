/**
 * `_example` — sentinel plugin used to exercise the discovery loop in
 * tests. Skipped by the production scanner (filename begins with `_`).
 *
 * Tests opt in via either:
 *   - `--include-hidden` CLI flag, or
 *   - `SHARKO_SCAN_LOAD_HIDDEN=1` env var.
 *
 * DO NOT add real upstream scanning logic here — that's V123-3.2
 * (CNCF Landscape) and V123-3.3 (EKS Blueprints).
 */
export const name = '_example';

export async function fetch(_ctx) {
  return [];
}
