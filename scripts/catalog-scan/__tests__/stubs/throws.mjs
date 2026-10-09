/**
 * Test stub: throws on fetch. Used by the 'plugin-error-isolated'
 * integration case to assert a single plugin's failure does NOT
 * abort the run — its sibling plugins still get to contribute.
 */
export const name = 'stub-throws';

export async function fetch(_ctx) {
  throw new Error('upstream blip');
}
