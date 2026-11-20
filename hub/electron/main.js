'use strict';
const path = require('node:path');
const fs = require('node:fs');
const { app, BrowserWindow, ipcMain, clipboard, dialog, shell } = require('electron');
const core = require('./core');
const { isAllowed } = require('./api');
const vscode = require('./vscode');

const LIVE_INTERVAL_MS = 1000;
let win = null;
let child = null;
let liveTimer = null;
let home = '';
let keys = { data_api_key: '', admin_api_key: '' };

function createWindow() {
  win = new BrowserWindow({
    width: 1280,
    height: 800,
    minWidth: 1080,
    minHeight: 680,
    backgroundColor: '#05070d',
    title: 'OmniHub',
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      sandbox: true,
      nodeIntegration: false,
    },
  });
  win.setMenuBarVisibility(false);
  win.on('closed', () => { win = null; });
  win.loadFile(path.join(__dirname, '..', 'frontend', 'dist', 'index.html'), { hash: process.env.OMNIHUB_ROUTE || '' });
}

function readLogTail(maxBytes = 64 * 1024) {
  const file = path.join(home, 'logs', 'bridge.log');
  if (!fs.existsSync(file)) return [];
  const stat = fs.statSync(file);
  const start = Math.max(0, stat.size - maxBytes);
  const fd = fs.openSync(file, 'r');
  try {
    const buf = Buffer.alloc(stat.size - start);
    fs.readSync(fd, buf, 0, buf.length, start);
    return buf.toString('utf8').split(/\r?\n/).filter(Boolean).slice(-400);
  } finally {
    fs.closeSync(fd);
  }
}

function startLive() {
  liveTimer = setInterval(async () => {
    if (!win || win.isDestroyed()) return;
    try {
      const r = await core.coreJSON('GET', '/bridge/statistics?period=session', keys.admin_api_key);
      if (r.ok) win.webContents.send('hub:live', { at: Date.now(), total: r.body.total, providers: r.body.providers || {} });
    } catch (_) { /* core restarting */ }
  }, LIVE_INTERVAL_MS);
}

function registerIPC() {
  ipcMain.handle('hub:status', async () => {
    const health = await core.coreJSON('GET', '/health').catch(() => null);
    return { running: !!(health && health.ok), endpoint: `${core.BASE}/v1` };
  });

  ipcMain.handle('hub:connection', () => ({
    endpoint: `${core.BASE}/v1`,
    keyMasked: core.maskKey(keys.data_api_key),
  }));

  ipcMain.handle('hub:copy', (_e, req) => {
    const what = req && req.what;
    if (what === 'key') clipboard.writeText(keys.data_api_key);
    else if (what === 'endpoint') clipboard.writeText(`${core.BASE}/v1`);
    else if (what === 'text' && typeof req.text === 'string' && req.text.length < 2_000_000) clipboard.writeText(req.text);
    else return false;
    return true;
  });

  ipcMain.handle('hub:info', () => ({
    version: app.getVersion(),
    endpoint: `${core.BASE}/v1`,
    home,
  }));

  // Generic admin call, restricted to the allow-list above.
  ipcMain.handle('hub:api', async (_e, req) => {
    const method = String((req && req.method) || 'GET').toUpperCase();
    const route = String((req && req.path) || '');
    if (!isAllowed(method, route)) return { ok: false, status: 403, body: { error: 'route not allowed' } };
    const body = req && req.body !== undefined ? JSON.stringify(req.body) : undefined;
    return core.coreJSON(method, route, keys.admin_api_key, body);
  });

  ipcMain.handle('hub:models', async () => {
    const r = await core.coreJSON('GET', '/v1/models', keys.data_api_key);
    if (!r.ok) return { ok: false, status: r.status, models: [] };
    const list = Array.isArray(r.body && r.body.data) ? r.body.data : [];
    return { ok: true, status: r.status, models: list.map((m) => ({ id: m.id, owned_by: m.owned_by || '' })) };
  });

  ipcMain.handle('hub:logs', () => readLogTail());

  // Opens the provider's own login page. Only https links are allowed.
  ipcMain.handle('hub:open-url', async (_e, req) => {
    let u;
    try { u = new URL(String(req && req.url)); } catch { return { ok: false }; }
    if (u.protocol !== 'https:') return { ok: false };
    await shell.openExternal(u.toString());
    return { ok: true };
  });

  // Writes the model list into VS Code's chatLanguageModels.json after a native confirmation.
  ipcMain.handle('hub:vscode-install', async (_e, req) => {
    const edition = req && req.edition === 'insiders' ? 'insiders' : 'stable';
    const target = vscode.targetPath(edition);
    const exp = await core.coreJSON('GET', '/bridge/vscode/export', keys.admin_api_key);
    if (!exp.ok) return { ok: false, error: `export failed: ${exp.status}` };
    const count = Array.isArray(exp.body.models) ? exp.body.models.length : 0;
    if (!count) return { ok: false, error: 'no verified models to export' };
    const choice = await dialog.showMessageBox(win, {
      type: 'question',
      buttons: ['Записать', 'Отмена'],
      defaultId: 0,
      cancelId: 1,
      title: 'Записать в VS Code',
      message: `Записать ${count} моделей в VS Code?`,
      detail: `Файл: ${target}\nСуществующий файл будет сохранён как резервная копия.`,
    });
    if (choice.response !== 0) return { ok: false, cancelled: true };
    const content = JSON.stringify(vscode.buildChatLanguageModels(exp.body, keys.data_api_key), null, 2);
    const backup = vscode.writeAtomic(target, content);
    return { ok: true, path: target, models: count, backup };
  });
}

async function boot() {
  home = process.env.OMNIHUB_DATA || path.join(app.getPath('userData'), 'core');
  const binary = core.coreBinaryPath(app);
  child = core.startCore(binary, home);
  child.on('exit', () => { child = null; });
  await core.waitForHealth();
  keys = await core.readKeys(binary, home);
  registerIPC();
  createWindow();
  startLive();
  if (process.env.OMNIHUB_SCREENSHOT) {
    // Dev-only: render once, save a PNG and exit, used for visual checks.
    const target = process.env.OMNIHUB_SCREENSHOT;
    win.webContents.once('did-finish-load', () => setTimeout(async () => {
      const img = await win.webContents.capturePage();
      fs.writeFileSync(target, img.toPNG());
      app.quit();
    }, 2500));
  }
}

app.whenReady().then(boot).catch((err) => {
  console.error(err);
  app.quit();
});

app.on('window-all-closed', () => app.quit());

app.on('before-quit', () => {
  if (liveTimer) clearInterval(liveTimer);
  if (child) child.kill();
});
