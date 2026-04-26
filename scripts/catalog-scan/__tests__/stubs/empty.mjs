/**
 * Test stub: returns an empty proposals array. Used by the
 * 'no-changes-no-output' integration case in catalog-scan.test.mjs.
 */
export const name = 'stub-empty';

export async function fetch(_ctx) {
  return [];
}
