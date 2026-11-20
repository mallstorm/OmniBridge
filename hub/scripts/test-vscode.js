'use strict';
const assert = require('node:assert');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const v = require('../electron/vscode');

const exp = {
  default_api_type: 'chat-completions',
  models: [
    { id: 'auto/fast', name: 'Auto Fast', url: 'http://127.0.0.1:4142/v1/chat/completions', toolCalling: true, vision: false, maxInputTokens: 128000 },
    { id: 'demo-pro', name: 'Demo Pro', url: 'http://127.0.0.1:4142/v1/chat/completions', toolCalling: false, vision: true },
    { id: '', url: 'x' },
  ],
};
const out = v.buildChatLanguageModels(exp, 'local-key');
assert.strictEqual(out.length, 1);
assert.strictEqual(out[0].vendor, 'customendpoint');
assert.strictEqual(out[0].apiKey, 'local-key');
assert.strictEqual(out[0].models.length, 2, 'entries without id are dropped');
assert.strictEqual(out[0].models[0].maxInputTokens, 128000);

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'vscode-test-'));
const file = path.join(dir, 'chatLanguageModels.json');
assert.strictEqual(v.writeAtomic(file, '[1]'), null, 'no backup for a new file');
const backup = v.writeAtomic(file, '[2]');
assert.ok(backup && fs.existsSync(backup), 'existing file is backed up');
assert.strictEqual(fs.readFileSync(file, 'utf8'), '[2]');
assert.strictEqual(fs.readFileSync(backup, 'utf8'), '[1]');
fs.rmSync(dir, { recursive: true, force: true });
console.log('VSCODE TESTS OK');
