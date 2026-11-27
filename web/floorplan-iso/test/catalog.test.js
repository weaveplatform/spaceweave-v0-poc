import { test } from 'node:test';
import assert from 'node:assert/strict';
import { loadCatalog, PLANS, LISTED } from '../js/plans/index.js';

const respond = (status, body) => ({ status, ok: status >= 200 && status < 300, json: async () => body });
const info = '/api/v1/info';
const list = '/api/v1/floorplans?limit=200';

function fakeFetch(routes) {
  const calls = [];
  const impl = async (url, init) => {
    calls.push({ url, init });
    return url in routes ? routes[url] : respond(404);
  };
  return { impl, calls };
}

test('an absent API explicitly enables the static viewer, with no catalogue initially', async () => {
  const { impl, calls } = fakeFetch({});
  assert.deepEqual(await loadCatalog(impl), []);
  assert.deepEqual(calls.map(c => c.url), [info, 'generated/catalog.json']);
});

test('only an absent info endpoint permits static catalogue reads', async () => {
  for (const status of [401, 403, 429, 500, 503]) {
    const { impl, calls } = fakeFetch({ [info]: respond(status, { detail: 'Service refused the request.' }) });
    await assert.rejects(loadCatalog(impl), err => err.status === status && err.message === 'Service refused the request.');
    assert.deepEqual(calls.map(c => c.url), [info]);
  }
  const offline = async () => { throw new TypeError('Network failure'); };
  await assert.rejects(loadCatalog(offline), /Network failure/);
});

test('a product problem response is authoritative even when its status is 404', async () => {
  const { impl, calls } = fakeFetch({
    [info]: respond(404, { type: 'about:blank', title: 'Not found', status: 404, detail: 'Product endpoint unavailable.' }),
  });
  await assert.rejects(loadCatalog(impl), err => err.status === 404);
  assert.deepEqual(calls.map(c => c.url), [info]);
});

test('list failure after API discovery never falls back to generated files', async () => {
  for (const status of [401, 403, 404, 500]) {
    const { impl, calls } = fakeFetch({ [info]: respond(200, {}), [list]: respond(status) });
    await assert.rejects(loadCatalog(impl), err => err.status === status);
    assert.deepEqual(calls.map(c => c.url), [info, list]);
  }
});

test('lists all REST pages, preserves metadata and never shadows a built-in', async () => {
  const { impl, calls } = fakeFetch({
    [info]: respond(200, {}),
    [list]: respond(200, {
      plans: [
        { id: 'a-1', title: 'A', status: 'draft', confidence: 0.8, summary: 'Small office', source: 'source.png', upload: 'original.dwg' },
        { id: 'b-2', status: 'approved' },
        { id: 'c-3', status: 'tracing' },
      ], nextCursor: 'page / two',
    }),
    [`${list}&cursor=page+%2F+two`]: respond(200, {
      plans: [
        { id: 'd-4', status: 'queued' },
        { id: 'f-6', status: 'failed', error: 'boom', costUsd: 0.5 },
        { id: PLANS[0].id, status: 'draft' },
      ],
    }),
    '/api/v1/floorplans/a-1/artifacts/model': respond(200, { WIDTH: 3 }),
    '/api/v1/floorplans/a-1/artifacts/report': respond(200, { caveats: ['Check scale'] }),
  });
  const plans = await loadCatalog(impl);
  assert.deepEqual(plans.map(p => [p.id, p.status]), [['a-1', 'draft'], ['b-2', 'approved'], ['c-3', 'tracing'], ['d-4', 'queued'], ['f-6', 'failed']]);
  assert.deepEqual(plans.filter(p => LISTED.has(p.status)).map(p => p.id), ['a-1', 'b-2', 'c-3', 'd-4']);
  assert.equal(plans[4].error, 'boom');
  assert.equal(plans[4].costUsd, 0.5);
  assert.equal(calls[0].init.cache, 'no-store');
  const [a, b] = plans;
  assert.equal(a.name, 'A');
  assert.equal(a.blurb, 'Small office');
  assert.equal(a.confidence, 0.8);
  assert.equal(a.staticOnly, false);
  assert.equal(a.dir, undefined);
  assert.equal(b.name, 'b-2');
  assert.equal(a.artifactUrl('original'), '/api/v1/floorplans/a-1/artifacts/original');
  assert.equal(a.fileUrl('sha / one'), '/api/v1/floorplans/a-1/files/sha%20%2F%20one');
  assert.deepEqual(await a.load(), { WIDTH: 3 });
  assert.deepEqual(await a.readArtifact('report'), { caveats: ['Check scale'] });
  await assert.rejects(b.load(), err => err.status === 404);
  assert.ok(calls.every(c => !c.url.startsWith('generated/')), 'REST reads must not probe files');
});

test('a denied or missing model never falls back to an artifact file', async () => {
  for (const status of [401, 403, 404, 500]) {
    const { impl, calls } = fakeFetch({
      [info]: respond(200, {}), [list]: respond(200, { plans: [{ id: 'a-1', status: 'draft' }] }),
      '/api/v1/floorplans/a-1/artifacts/model': respond(status),
    });
    const [plan] = await loadCatalog(impl);
    await assert.rejects(plan.load(), err => err.status === status);
    assert.ok(calls.every(c => !c.url.startsWith('generated/')));
  }
});

test('a repeated pagination cursor is reported instead of polling forever', async () => {
  const { impl } = fakeFetch({
    [info]: respond(200, {}), [list]: respond(200, { plans: [], nextCursor: 'same' }),
    [`${list}&cursor=same`]: respond(200, { plans: [], nextCursor: 'same' }),
  });
  await assert.rejects(loadCatalog(impl), /repeated page cursor/);
});

test('an empty REST list is empty', async () => {
  const { impl } = fakeFetch({ [info]: respond(200, {}), [list]: respond(200, {}) });
  assert.deepEqual(await loadCatalog(impl), []);
});

test('static-only viewer keeps watched plans and their evidence usable', async () => {
  const { impl, calls } = fakeFetch({
    'generated/catalog.json': respond(200, { plans: [{
      id: 'a-1', status: 'draft', source: 'source.png', upload: 'original.dwg', drawing: 'drawing.svg',
      files: [{ sha256: 'abc', upload: 'files/spaces-abc.dwg' }],
    }] }),
    'generated/a-1/plan.json': respond(200, { WIDTH: 3 }),
    'generated/a-1/report.json': respond(200, { caveats: [] }),
  });
  const [plan] = await loadCatalog(impl);
  assert.equal(plan.staticOnly, true);
  assert.equal(plan.artifactUrl('overlay'), 'generated/a-1/overlay.png');
  assert.equal(plan.artifactUrl('original'), 'generated/a-1/original.dwg');
  assert.equal(plan.artifactUrl('source'), 'generated/a-1/source.png');
  assert.equal(plan.fileUrl('abc'), 'generated/a-1/files/spaces-abc.dwg');
  assert.deepEqual(await plan.load(), { WIDTH: 3 });
  assert.deepEqual(await plan.readArtifact('report'), { caveats: [] });
  assert.deepEqual(calls.slice(0, 2).map(c => c.url), [info, 'generated/catalog.json']);
});

test('static catalogue failures remain errors', async () => {
  const { impl } = fakeFetch({ 'generated/catalog.json': respond(500) });
  await assert.rejects(loadCatalog(impl), /HTTP 500/);
});
