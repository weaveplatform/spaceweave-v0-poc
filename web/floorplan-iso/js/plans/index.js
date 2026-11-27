// Built-in examples load as modules. Imported floorplans use the product REST
// API; only an absent info endpoint enables the explicit static-only viewer.
import { ApiError, artifactUrl, createApi, fileUrl } from '../api.js';

export const PLANS = [
  { id: 'office', name: 'Office', blurb: 'RoomSketcher sample · 19 × 11 m', load: () => import('./roomsketcher-office.js') },
  { id: 'clinic', name: 'Clinic', blurb: 'Physical therapy practice (SmartDraw sample)', load: () => import('./pt-clinic.js') },
  { id: 'suites', name: 'Suites', blurb: 'New Urban Suites, third floor', load: () => import('./urban-suites.js') },
  { id: 'corporate', name: 'Corporate', blurb: 'Open-plan floor with private offices', load: () => import('./corporate-floor.js') },
];

export const DEFAULT_PLAN = 'office';

// Statuses the picker lists. queued, converting and tracing show as disabled
// placeholders so a dropped plan is visibly on its way; failed plans have
// nothing to show and appear only in the upload pipeline.
export const LISTED = new Set(['queued', 'converting', 'tracing', 'draft', 'approved']);
export const READY = new Set(['draft', 'approved']);

export const GENERATED = 'generated/';
const STATIC_ARTIFACTS = {
  model: 'plan.json', trace: 'trace.json', report: 'report.json',
  source: 'source.png', overlay: 'overlay.png', drawing: 'drawing.svg',
};

async function staticCatalog(fetchImpl) {
  const res = await fetchImpl(`${GENERATED}catalog.json`, { cache: 'no-store' });
  if (res.status === 404) return [];
  if (!res.ok) throw new ApiError(`catalog.json: HTTP ${res.status}`, res.status);
  return (await res.json()).plans ?? [];
}

// Read all pages before replacing the picker, so partial results don't hide
// floors. A permission or server failure must never fall through to file reads.
export async function loadCatalog(fetchImpl = globalThis.fetch) {
  const api = createApi(fetchImpl);
  let staticOnly = false;
  try {
    await api.info();
  } catch (err) {
    if (err.status !== 404 || Object.keys(err.problem ?? {}).length !== 0) throw err;
    staticOnly = true;
  }
  let plans;
  if (staticOnly) plans = await staticCatalog(fetchImpl);
  else {
    plans = [];
    let cursor;
    const seen = new Set();
    do {
      const page = await api.list({ cursor });
      plans.push(...(page.plans ?? []));
      cursor = page.nextCursor;
      if (cursor && seen.has(cursor)) throw new Error('Floorplan list returned a repeated page cursor.');
      if (cursor) seen.add(cursor);
    } while (cursor);
  }
  const builtIn = new Set(PLANS.map(p => p.id));
  return plans.filter(p => !builtIn.has(p.id)).map(p => {
    const dir = staticOnly ? `${GENERATED}${encodeURIComponent(p.id)}/` : undefined;
    const artifact = kind => staticOnly
      ? `${dir}${kind === 'original' ? p.upload : kind === 'source' ? p.source : kind === 'drawing' ? p.drawing : STATIC_ARTIFACTS[kind]}`
      : artifactUrl(p.id, kind);
    const readArtifact = async kind => {
      if (!staticOnly) return api.artifact(p.id, kind);
      const url = artifact(kind);
      const res = await fetchImpl(url, { cache: 'no-store' });
      if (!res.ok) throw new ApiError(`${url}: HTTP ${res.status}`, res.status);
      return res.json();
    };
    return {
      id: p.id,
      name: p.title || p.id,
      blurb: p.summary || 'Traced automatically',
      status: p.status,
      confidence: p.confidence,
      costUsd: p.costUsd,
      tracer: p.tracer,
      attempts: p.attempts,
      note: p.note,
      error: p.error,
      progress: p.progress,
      updatedAt: p.updatedAt,
      format: p.format,
      source: p.source,
      upload: p.upload,
      files: p.files ?? [],
      drawing: p.drawing,
      scale: p.scale,
      generated: true,
      staticOnly,
      dir,
      artifactUrl: artifact,
      fileUrl: sha => staticOnly
        ? `${dir}${p.files?.find(file => file.sha256 === sha)?.upload ?? ''}`
        : fileUrl(p.id, sha),
      readArtifact,
      load: () => readArtifact('model'),
    };
  });
}
