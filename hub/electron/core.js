'use strict';
// Core process management and loopback calls for the OmniHub shell.
// The renderer never talks to the Core directly and never sees raw keys.

const path = require('node:path');
const fs = require('node:fs');
const { spawn, execFile } = require('node:child_process');

const LISTEN = process.env.OMNIHUB_CORE_LISTEN || '127.0.0.1:4142';
const BASE = `http://${LISTEN}`;

function coreBinaryPath(app) {
  const name = process.platform === 'win32' ? 'omnibridge.exe' : 'omnibridge';
  return app.isPackaged
    ? path.join(process.resourcesPath, 'core', name)
    : path.join(__dirname, '..', 'resources', 'core', name);
}

function startCore(binary, home) {
  fs.mkdirSync(home, { recursive: true });
  const child = spawn(binary, [], {
    env: { ...process.env, OMNIBRIDGE_HOME: home, OMNIBRIDGE_HEADLESS: '1' },
    windowsHide: true,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  child.stderr.on('data', () => {});
  child.stdout.on('data', () => {});
  return child;
}

async function waitForHealth(timeoutMs = 20000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const r = await fetch(`${BASE}/health`);
      if (r.ok) return true;
    } catch (_) { /* not up yet */ }
    await new Promise((res) => setTimeout(res, 150));
  }
  throw new Error('OmniBridge Core did not become ready');
}

// Keys are read through the Core binary itself, in the same user context,
// so DPAPI-protected secrets stay readable and never pass through the UI.
function readKeys(binary, home) {
  return new Promise((resolve, reject) => {
    execFile(binary, ['keys'], {
      env: { ...process.env, OMNIBRIDGE_HOME: home },
      windowsHide: true,
      timeout: 15000,
    }, (err, stdout) => {
      if (err) return reject(err);
      try { resolve(JSON.parse(stdout)); } catch (e) { reject(e); }
    });
  });
}

async function coreJSON(method, route, token, payload) {
  const headers = token ? { Authorization: `Bearer ${token}` } : {};
  if (payload !== undefined) headers['Content-Type'] = 'application/json';
  const r = await fetch(`${BASE}${route}`, { method, headers, body: payload });
  const text = await r.text();
  let body;
  try { body = JSON.parse(text); } catch (_) { body = text; }
  return { status: r.status, ok: r.ok, body };
}

function maskKey(key) {
  if (!key) return '';
  return key.length <= 8 ? '••••' : `${key.slice(0, 4)}••••${key.slice(-4)}`;
}

module.exports = { BASE, LISTEN, coreBinaryPath, startCore, waitForHealth, readKeys, coreJSON, maskKey };
