'use strict';
// Headless check of the Hub <-> Core contract without a window:
// start Core, wait for health, read keys, call /v1/models with the data key.
const path = require('node:path');
const fs = require('node:fs');
const os = require('node:os');
const core = require('../electron/core');

const binary = path.join(__dirname, '..', 'resources', 'core', process.platform === 'win32' ? 'omnibridge.exe' : 'omnibridge');
const home = fs.mkdtempSync(path.join(os.tmpdir(), 'omnihub-smoke-'));

(async () => {
  const child = core.startCore(binary, home);
  try {
    await core.waitForHealth(20000);
    const health = await core.coreJSON('GET', '/health');
    if (!health.ok) throw new Error(`/health ${health.status}`);
    const keys = await core.readKeys(binary, home);
    if (!keys.data_api_key || !keys.admin_api_key) throw new Error('keys missing');
    const models = await core.coreJSON('GET', '/v1/models', keys.data_api_key);
    if (!models.ok) throw new Error(`/v1/models ${models.status}`);
    const unauth = await core.coreJSON('GET', '/v1/models');
    if (unauth.status !== 401) throw new Error(`expected 401 without key, got ${unauth.status}`);
    console.log('SMOKE OK', JSON.stringify({ health: health.status, models: models.body.data ? models.body.data.length : 0, keyMasked: core.maskKey(keys.data_api_key) }));
  } finally {
    child.kill();
    fs.rmSync(home, { recursive: true, force: true });
  }
})().catch((err) => {
  console.error('SMOKE FAILED:', err.message);
  process.exit(1);
});
