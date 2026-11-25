'use strict';
// End-to-end check of the Hub contract with a fake upstream provider:
// add provider -> models appear -> hot disable -> models disappear -> chat tokens counted.
const assert = require('node:assert/strict');
const http = require('node:http');
const path = require('node:path');
const fs = require('node:fs');
const os = require('node:os');
const core = require('../electron/core');

const binary = path.join(__dirname, '..', 'resources', 'core', process.platform === 'win32' ? 'omnibridge.exe' : 'omnibridge');
const home = fs.mkdtempSync(path.join(os.tmpdir(), 'omnihub-e2e-'));

function fakeUpstream() {
  return new Promise((resolve) => {
    const srv = http.createServer((req, res) => {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => {
        res.setHeader('Content-Type', 'application/json');
        if (req.url.endsWith('/models')) {
          res.end(JSON.stringify({ object: 'list', data: [{ id: 'demo-fast', object: 'model' }, { id: 'demo-pro', object: 'model' }] }));
        } else if (req.url.endsWith('/chat/completions')) {
          srv.lastModel = JSON.parse(body).model;
          res.end(JSON.stringify({ id: 'x', object: 'chat.completion', model: JSON.parse(body).model, choices: [{ index: 0, message: { role: 'assistant', content: 'hi' }, finish_reason: 'stop' }], usage: { prompt_tokens: 120, completion_tokens: 30, total_tokens: 150 } }));
        } else {
          res.statusCode = 404;
          res.end('{}');
        }
      });
    });
    srv.listen(0, '127.0.0.1', () => resolve(srv));
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function waitFor(fn, label, timeoutMs = 15000) {
  const end = Date.now() + timeoutMs;
  while (Date.now() < end) {
    if (await fn()) return;
    await sleep(250);
  }
  throw new Error('timeout waiting for ' + label);
}

(async () => {
  const upstream = await fakeUpstream();
  const up = `http://127.0.0.1:${upstream.address().port}/v1`;
  const child = core.startCore(binary, home);
  try {
    await core.waitForHealth(20000);
    const keys = await core.readKeys(binary, home);
    const admin = keys.admin_api_key;
    const data = keys.data_api_key;

    const add = await core.coreJSON('POST', '/bridge/providers', admin, JSON.stringify({ name: 'Demo', kind: 'custom', base_url: up, api_key: 'upstream-secret', auth_type: 'none', allow_unverified: true }));
    if (!add.ok) throw new Error(`add provider ${add.status} ${JSON.stringify(add.body)}`);
    const providerId = add.body.id || 'demo';
    console.log('provider added:', providerId);

    const hasDemo = async () => {
      const m = await core.coreJSON('GET', '/v1/models', data);
      return m.ok && m.body.data.some((x) => String(x.id).includes('demo-fast'));
    };
    await waitFor(hasDemo, 'demo models to appear');
    console.log('models visible: yes');

    const off = await core.coreJSON('PUT', `/bridge/providers/${providerId}/enabled`, admin, JSON.stringify({ enabled: false }));
    if (!off.ok) throw new Error(`disable ${off.status}`);
    await waitFor(async () => !(await hasDemo()), 'demo models to disappear');
    console.log('hot disable removes models: yes');

    const on = await core.coreJSON('PUT', `/bridge/providers/${providerId}/enabled`, admin, JSON.stringify({ enabled: true }));
    if (!on.ok) throw new Error(`enable ${on.status}`);
    await waitFor(hasDemo, 'demo models to return');
    console.log('hot enable restores models: yes');

    for (const target of ['demo-fast', 'demo-pro']) {
      const selection = await core.coreJSON('PUT', '/bridge/router/active-model', admin, JSON.stringify({ model: providerId + '/' + target }));
      assert.equal(selection.status, 200);
      const routed = await core.coreJSON('POST', '/v1/chat/completions', data, JSON.stringify({ model: 'hub-active', messages: [{ role: 'user', content: 'hi' }] }));
      assert.equal(routed.status, 200);
      assert.equal(upstream.lastModel, target);
    }
    const chat = await core.coreJSON('POST', '/v1/chat/completions', data, JSON.stringify({ model: 'demo-fast', messages: [{ role: 'user', content: 'hi' }] }));
    if (!chat.ok) throw new Error(`chat ${chat.status} ${JSON.stringify(chat.body)}`);
    const st = await core.coreJSON('GET', '/bridge/statistics?period=session', admin);
    const total = st.body.total.total_tokens;
    if (!(total >= 150)) throw new Error(`tokens not counted: ${total}`);
    console.log('tokens counted in session stats:', total);

    const leak = JSON.stringify(st.body).includes('upstream-secret');
    if (leak) throw new Error('upstream key leaked into statistics');
    console.log('E2E OK');
  } finally {
    child.kill();
    upstream.close();
    fs.rmSync(home, { recursive: true, force: true });
  }
})().catch((e) => {
  console.error('E2E FAILED:', e.message);
  process.exit(1);
});
