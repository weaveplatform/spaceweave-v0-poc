// Client for the supported product REST API. A plain static viewer has no
// /api/v1/info route; callers may explicitly detect that 404 before using files.
export const API_ROOT = '/api/v1/';

// Must match the HTTP adapter. A page on another origin can't send this
// header without a CORS preflight, which the local server never approves.
export const REQUEST_HEADER = 'X-Spaceweave';

export class ApiError extends Error {
  constructor(message, status, problem = {}) {
    super(message);
    this.status = status;
    this.problem = problem;
  }
}

const floorplanPath = id => `floorplans/${encodeURIComponent(id)}`;
export const artifactUrl = (id, artifact) => `${API_ROOT}${floorplanPath(id)}/artifacts/${encodeURIComponent(artifact)}`;
export const fileUrl = (id, sha) => `${API_ROOT}${floorplanPath(id)}/files/${encodeURIComponent(sha)}`;

export function createApi(fetchImpl = globalThis.fetch) {
  async function call(path, init = {}) {
    const writes = init.method && !['GET', 'HEAD'].includes(init.method);
    const headers = writes ? { [REQUEST_HEADER]: '1', ...init.headers } : init.headers;
    const res = await fetchImpl(`${API_ROOT}${path}`, { cache: 'no-store', ...init, headers });
    let body = {};
    try {
      body = await res.json();
    } catch { /* A static server's 404 page is HTML. */ }
    if (!res.ok) throw new ApiError(body?.detail || body?.title || `HTTP ${res.status}`, res.status, body);
    return body;
  }
  return {
    info: () => call('info'),
    list({ limit = 200, cursor } = {}) {
      const query = new URLSearchParams({ limit: String(limit) });
      if (cursor) query.set('cursor', cursor);
      return call(`floorplans?${query}`);
    },
    get: id => call(floorplanPath(id)),
    processing: id => call(`${floorplanPath(id)}/processing`),
    artifact: (id, artifact) => call(`${floorplanPath(id)}/artifacts/${encodeURIComponent(artifact)}`),
    upload(file, name = file.name) {
      const form = new FormData();
      form.append('image', file, name);
      return call('floorplans', { method: 'POST', body: form });
    },
    attach(id, file, role, name = file.name) {
      const form = new FormData();
      form.append('role', role);
      form.append('file', file, name);
      return call(`${floorplanPath(id)}/files`, { method: 'POST', body: form });
    },
    detach: (id, sha) => call(`${floorplanPath(id)}/files/${encodeURIComponent(sha)}`, {
      method: 'DELETE',
    }),
    review: (id, action, note = '') => call(`${floorplanPath(id)}/${encodeURIComponent(action)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ note }),
    }),
    profile: name => call(`layer-profiles/${encodeURIComponent(name)}`),
    saveProfile: (name, profile) => call(`layer-profiles/${encodeURIComponent(name)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(profile),
    }),
  };
}
