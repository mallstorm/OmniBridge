'use strict';
// Allow-listed bridge between the UI and the Core admin API.
// Only the routes below are reachable from the renderer; keys never leave the main process.

const ID = '[^/?]+';
const ALLOW = [
  ['PUT', /^\/bridge\/router\/active-model$/],
  ['GET', new RegExp(`^/bridge/(status|providers|accounts|account-catalog|catalog|router|integrations|vscode|vscode/export|connections/health|resilience|quota-cost|observability)$`)],
  ['GET', /^\/bridge\/statistics\?period=(session|24h|7d|30d|all)$/],
  ['GET', new RegExp(`^/bridge/accounts/${ID}/usage$`)],
  ['PUT', new RegExp(`^/bridge/(providers|accounts)/${ID}/enabled$`)],
  ['POST', /^\/bridge\/providers$/],
  ['POST', /^\/bridge\/accounts$/],
  ['POST', new RegExp(`^/bridge/accounts/${ID}/login/start$`)],
  ['POST', new RegExp(`^/bridge/accounts/${ID}/logout$`)],
  ['GET', /^\/bridge\/accounts\/login\/status\?attempt=[A-Za-z0-9_-]+$/],
  ['POST', new RegExp(`^/bridge/(providers|accounts)/${ID}/refresh$`)],
  ['DELETE', new RegExp(`^/bridge/(providers|accounts)/${ID}$`)],
];

function isAllowed(method, route) {
  return ALLOW.some(([m, re]) => m === method && re.test(route));
}

module.exports = { isAllowed };
