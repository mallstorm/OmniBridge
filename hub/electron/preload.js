'use strict';
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('hub', {
  status: () => ipcRenderer.invoke('hub:status'),
  connection: () => ipcRenderer.invoke('hub:connection'),
  models: () => ipcRenderer.invoke('hub:models'),
  api: (req) => ipcRenderer.invoke('hub:api', req),
  logs: () => ipcRenderer.invoke('hub:logs'),
  copy: (req) => ipcRenderer.invoke('hub:copy', req),
  info: () => ipcRenderer.invoke('hub:info'),
  vscodeInstall: (req) => ipcRenderer.invoke('hub:vscode-install', req),
  openUrl: (url) => ipcRenderer.invoke('hub:open-url', { url }),
  onLive: (cb) => {
    const listener = (_e, payload) => cb(payload);
    ipcRenderer.on('hub:live', listener);
    return () => ipcRenderer.removeListener('hub:live', listener);
  },
});
