import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ApiError, artifactUrl, createApi, fileUrl } from '../js/api.js';

function client(status = 200, body = {}) {
  const calls = [];
  const api = createApi(async (url, init) => {
    calls.push({ url, init });
    return { ok: status >= 200 && status < 300, status, json: async () => body };
  });
  return { api, calls };
}

test('product reads use versioned REST routes and encode resource identifiers', async () => {
  const { api, calls } = client();
  await api.info();
  await api.list({ limit: 17, cursor: 'next / page' });
  await api.get('floor / one');
  await api.processing('floor / one');
  await api.artifact('floor / one', 'report');
  await api.profile('client one');
  assert.deepEqual(calls.map(c => c.url), [
    '/api/v1/info',
    '/api/v1/floorplans?limit=17&cursor=next+%2F+page',
    '/api/v1/floorplans/floor%20%2F%20one',
    '/api/v1/floorplans/floor%20%2F%20one/processing',
    '/api/v1/floorplans/floor%20%2F%20one/artifacts/report',
    '/api/v1/layer-profiles/client%20one',
  ]);
  for (const { init } of calls) {
    assert.equal(init.cache, 'no-store');
    assert.equal(init.headers, undefined, 'reads need no local mutation header or bearer token');
  }
  assert.equal(artifactUrl('floor / one', 'overlay'), '/api/v1/floorplans/floor%20%2F%20one/artifacts/overlay');
  assert.equal(fileUrl('floor / one', 'sha / one'), '/api/v1/floorplans/floor%20%2F%20one/files/sha%20%2F%20one');
});

test('profile writes preserve JSON and the local mutation header', async () => {
  const { api, calls } = client();
  await api.saveProfile('client one', { rules: [] });
  assert.equal(calls[0].url, '/api/v1/layer-profiles/client%20one');
  assert.equal(calls[0].init.method, 'PUT');
  assert.deepEqual(calls[0].init.headers, { 'X-Spaceweave': '1', 'Content-Type': 'application/json' });
  assert.deepEqual(JSON.parse(calls[0].init.body), { rules: [] });
});

test('problem responses preserve diagnostic fields and prefer a useful detail', async () => {
  const problem = { type: 'about:blank', title: 'Forbidden', status: 403, detail: 'This floor is outside your scope.' };
  await assert.rejects(client(403, problem).api.get('one'), err => {
    assert.ok(err instanceof ApiError);
    assert.equal(err.status, 403);
    assert.equal(err.message, problem.detail);
    assert.deepEqual(err.problem, problem);
    return true;
  });
  await assert.rejects(client(503, { title: 'Service unavailable' }).api.info(), /Service unavailable/);
  await assert.rejects(client(500, null).api.info(), /HTTP 500/);
});

test('an HTML failure remains an HTTP error rather than hiding an unavailable API', async () => {
  const api = createApi(async () => ({ status: 404, ok: false, json: async () => { throw new SyntaxError('HTML'); } }));
  await assert.rejects(api.info(), err => err instanceof ApiError && err.status === 404);
});
