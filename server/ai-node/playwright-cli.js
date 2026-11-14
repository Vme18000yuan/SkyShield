#!/usr/bin/env node
// Configure the bundled browser for the existing AI sandbox and outbound relay.
// Explicit user configuration always takes precedence.
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const args = process.argv.slice(2);
const explicit = process.env.PLAYWRIGHT_MCP_CONFIG ||
  args.some(arg => arg === '--config' || arg.startsWith('--config=')) ||
  fs.existsSync(path.join(process.cwd(), '.playwright', 'cli.config.json'));
const env = { ...process.env };
let temporary;

try {
  if (!explicit) {
    temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'skyshield-playwright-'));
    const launch = {
      executablePath: '/usr/lib/chromium/chromium',
      chromiumSandbox: false,
      args: ['--single-process', '--no-zygote', '--disable-gpu'],
    };
    const context = {};
    const proxyUrl = env.HTTPS_PROXY || env.HTTP_PROXY || env.https_proxy || env.http_proxy;
    if (proxyUrl) {
      const proxy = new URL(proxyUrl);
      if (!['http:', 'https:', 'socks5:'].includes(proxy.protocol) || !proxy.host)
        throw new Error('invalid browser proxy');
      const settings = { server: `${proxy.protocol}//${proxy.host}`, bypass: '<-loopback>' };
      if (proxy.username) settings.username = decodeURIComponent(proxy.username);
      if (proxy.password) settings.password = decodeURIComponent(proxy.password);
      launch.proxy = settings;
      context.proxy = settings;
    }
    if (env.SSL_CERT_FILE) context.ignoreHTTPSErrors = true;
    env.PLAYWRIGHT_MCP_CONFIG = path.join(temporary, 'config.json');
    fs.writeFileSync(env.PLAYWRIGHT_MCP_CONFIG, JSON.stringify({ browser: { launchOptions: launch, contextOptions: context } }), { mode: 0o600 });
  }
  const cli = '/usr/local/lib/skyshield-ai-node/node_modules/@playwright/cli/playwright-cli.js';
  const result = spawnSync(process.execPath, [cli, ...args], { stdio: 'inherit', env });
  if (result.error) throw result.error;
  process.exitCode = result.status === null ? 1 : result.status;
} catch (error) {
  console.error('playwright-cli: ' + error.message);
  process.exitCode = 1;
} finally {
  if (temporary) fs.rmSync(temporary, { recursive: true, force: true });
}
