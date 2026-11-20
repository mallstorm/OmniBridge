'use strict';
// Dev-only: seeds a data dir with a demo provider and traffic, then keeps the
// fake upstream alive so screenshots can run against a populated Hub.
const http = require('node:http');
const path = require('node:path');
const fs = require('node:fs');
const core = require('../electron/core');

const dataDir = process.argv[2];
if (!dataDir) { console.error('usage: seed-demo.js <dataDir>'); process.exit(2); }
const binary = path.join(__dirname, '..', 'resources', 'core', process.platform === 'win32' ? 'omnibridge.exe' : 'omnibridge');
fs.mkdirSync(dataDir, { recursive: true });

const srv = http.createServer((req, res) => {
  res.setHeader('Content-Type', 'application/json');
  if (req.url.endsWith('/models')) {
    res.end(JSON.stringify({ object: 'list', data: ['demo-fast', 'demo-pro', 'demo-vision'].map((id) => ({ id, object: 'model' })) }));
  } else {
    res.end(JSON.stringify({ id: 'x', object: 'chat.completion', model: 'demo-fast', choices: [{ index: 0, message: { role: 'assistant', content: 'ok' }, finish_reason: 'stop' }], usage: { prompt_tokens: 900, completion_tokens: 260, total_tokens: 1160 } }));
  }
});

srv.listen(0, '127.0.0.1', async () => {
  const up = `http://127.0.0.1:${srv.address().port}/v1`;
  const child = core.startCore(binary, dataDir);
  try {
    await core.waitForHealth(20000);
    const k = await core.readKeys(binary, dataDir);
    const add = await core.coreJSON('POST', '/bridge/providers', k.admin_api_key, JSON.stringify({ name: 'Demo Provider', kind: 'custom', base_url: up, api_key: 'upstream-secret', auth_type: 'none', allow_unverified: true }));
    if (!add.ok) throw new Error('add failed ' + add.status);
    await new Promise((r) => setTimeout(r, 2500));
    for (let i = 0; i < 6; i++) {
      await core.coreJSON('POST', '/v1/chat/completions', k.data_api_key, JSON.stringify({ model: 'demo-fast', messages: [{ role: 'user', content: 'hi' }] }));
    }
    console.log('SEEDED', add.body.id);
  } finally {
    child.kill();
    await new Promise((r) => setTimeout(r, 800));
    console.log('UPSTREAM UP', up);
  }
});
process.on('SIGTERM', () => { srv.close(); process.exit(0); });
