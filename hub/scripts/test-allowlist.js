'use strict';
// The renderer may only reach the routes listed in electron/api.js.
const assert = require('node:assert');
const { isAllowed } = require('../electron/api');

const allowed = [
  ['GET', '/bridge/providers'],
  ['PUT', '/bridge/providers/openai-1/enabled'],
  ['PUT', '/bridge/accounts/chatgpt-codex-abc/enabled'],
  ['POST', '/bridge/providers'],
  ['POST', '/bridge/accounts'],
  ['POST', '/bridge/accounts/chatgpt-codex-abc/login/start'],
  ['GET', '/bridge/accounts/login/status?attempt=att_123-XY'],
  ['POST', '/bridge/accounts/chatgpt-codex-abc/logout'],
  ['DELETE', '/bridge/accounts/chatgpt-codex-abc'],
  ['GET', '/bridge/account-catalog'],
  ['GET', '/bridge/statistics?period=24h'],
];
for (const [m, r] of allowed) assert.ok(isAllowed(m, r), `should allow ${m} ${r}`);

const denied = [
  ['POST', '/bridge/detect'],
  ['POST', '/control/shutdown'],
  ['PUT', '/bridge/router'],
  ['GET', '/bridge/accounts/login/status?attempt=../../x'],
  ['GET', '/bridge/accounts/login/status?attempt=a&other=1'],
  ['POST', '/bridge/accounts/a/b/login/start'],
  ['DELETE', '/bridge/providers'],
  ['GET', '/bridge/providers/../../control/shutdown'],
];
for (const [m, r] of denied) assert.ok(!isAllowed(m, r), `should deny ${m} ${r}`);

console.log(`allowlist OK: ${allowed.length} allowed, ${denied.length} denied`);
