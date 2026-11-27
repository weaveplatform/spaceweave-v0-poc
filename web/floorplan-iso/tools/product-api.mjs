// Read-only product client used by browser trials. API failures stop a trial;
// these tools do not bypass the product contract by reading generated files.
import { createApi } from '../js/api.js';

export function productApi(site, fetchImpl = globalThis.fetch) {
  return createApi((path, init) => fetchImpl(new URL(path, site), init));
}

export async function listFloorplans(api) {
  const plans = [], seen = new Set();
  let cursor;
  do {
    const page = await api.list({ cursor });
    plans.push(...page.plans);
    cursor = page.nextCursor;
    if (cursor && seen.has(cursor)) throw Error('Floorplan list returned a repeated page cursor.');
    if (cursor) seen.add(cursor);
  } while (cursor);
  return plans;
}
