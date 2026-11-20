'use strict';
// Builds VS Code's chatLanguageModels.json from the Core VS Code export and
// writes it atomically. Existing files are backed up before any change.

const path = require('node:path');
const fs = require('node:fs');

function userDir(edition) {
  const appData = process.env.APPDATA || path.join(require('node:os').homedir(), 'AppData', 'Roaming');
  return path.join(appData, edition === 'insiders' ? 'Code - Insiders' : 'Code', 'User');
}

function targetPath(edition) {
  return path.join(userDir(edition), 'chatLanguageModels.json');
}

// exportBody is the Core response of GET /bridge/vscode/export.
function buildChatLanguageModels(exportBody, localKey) {
  const models = (exportBody && Array.isArray(exportBody.models) ? exportBody.models : [])
    .filter((m) => m && m.id && m.url)
    .map((m) => ({
      id: m.id,
      name: m.name || m.id,
      url: m.url,
      toolCalling: !!m.toolCalling,
      vision: !!m.vision,
      thinking: !!m.thinking,
      maxInputTokens: m.maxInputTokens || undefined,
      maxOutputTokens: m.maxOutputTokens || undefined,
    }));
  return [
    {
      name: 'OmniHub',
      vendor: 'customendpoint',
      apiKey: localKey,
      apiType: (exportBody && exportBody.default_api_type) || 'chat-completions',
      models,
    },
  ];
}

// Writes via a temp file and rename; keeps a timestamped backup of any previous file.
function writeAtomic(file, content) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  let backup = null;
  if (fs.existsSync(file)) {
    backup = `${file}.bak-${new Date().toISOString().replace(/[:.]/g, '-')}`;
    fs.copyFileSync(file, backup);
  }
  const tmp = `${file}.tmp-${process.pid}`;
  fs.writeFileSync(tmp, content, { encoding: 'utf8', mode: 0o600 });
  fs.renameSync(tmp, file);
  return backup;
}

module.exports = { userDir, targetPath, buildChatLanguageModels, writeAtomic };
