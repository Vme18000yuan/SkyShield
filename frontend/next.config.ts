import type { NextConfig } from "next";
import createNextIntlPlugin from "next-intl/plugin";
import {
  assertProductionRuntimeFlags,
  assertVercelDemoHasNoBackend,
  getRequiredVercelBackendOrigin,
} from "./lib/production-runtime-config";

const withNextIntl = createNextIntlPlugin('./i18n/request.ts');

// Check if running on Vercel
const isVercel = process.env.VERCEL === '1';
const demoSite = process.env.DEMO_SITE === 'true'
const useMock = process.env.NEXT_PUBLIC_USE_MOCK === 'true'
const skipAuth = process.env.NEXT_PUBLIC_SKIP_AUTH === 'true'
// A separate output directory lets the local launcher validate a new build
// while the currently running frontend continues to serve its existing one.
const localStageDir = process.env.SKYSHIELD_NEXT_STAGE_DIR
if (localStageDir && !/^\.next-skyshield-stage-[a-zA-Z0-9-]+$/.test(localStageDir)) {
  throw new Error('SKYSHIELD_NEXT_STAGE_DIR must be a local staging directory name')
}

assertProductionRuntimeFlags({ useMock, skipAuth, demoSite, isVercel })
if (demoSite && process.env.NODE_ENV === "production") {
  assertVercelDemoHasNoBackend()
}
const vercelBackendOrigin = isVercel && process.env.NODE_ENV === "production" && !demoSite
  ? getRequiredVercelBackendOrigin()
  : null

const nextConfig: NextConfig = {
  ...(localStageDir ? { distDir: localStageDir } : {}),
  // A staged build must not rewrite the developer's main tsconfig.json.
  ...(localStageDir ? { typescript: { tsconfigPath: `tsconfig.${localStageDir}.json` } } : {}),
  // Docker copies the standalone bundle. Local staged deployments use
  // `next start` with node_modules and do not need that extra packaging.
  ...(isVercel || localStageDir ? {} : { output: 'standalone' }),
  // Keep output tracing inside this frontend package even when a parent folder
  // contains another lockfile from the developer workspace.
  outputFileTracingRoot: process.cwd(),
  // Disable Next.js automatic add/remove trailing slash behavior
  // Let us manually control URL format
  skipTrailingSlashRedirect: true,
  // Don't interrupt production build due to ESLint errors (keep lint in dev environment)
  eslint: {
    ignoreDuringBuilds: true,
  },
  // Allow local and LAN browser smoke origins to request dev assets.
  allowedDevOrigins: ['localhost', '127.0.0.1', '::1', '192.168.*.*', '10.*.*.*', '172.16.*.*'],

  // Optimize package imports to reduce bundle size and improve performance
  experimental: {
    // Keep local single-machine production builds within development hosts'
    // memory budget; container builds retain their existing worker settings.
    ...(process.env.TIANQIONG_LOCAL_MODE === "true" ? { cpus: 2 } : {}),
    optimizePackageImports: [
      // Icons
      '@tabler/icons-react',
      // Charts
      'recharts',
      // 3D / WebGL
      'three',
      'postprocessing',
      // Diagram rendering
      'mermaid',
      // Animation
      'framer-motion',
      'gsap',
      'motion',
      // Flow editor
      '@xyflow/react',
      // Code editor
      '@codemirror/commands',
      '@codemirror/lang-yaml',
      '@codemirror/language',
      '@codemirror/state',
      '@codemirror/theme-one-dark',
      '@codemirror/view',
      'codemirror',
      // Terminal emulator
      '@xterm/xterm',
      '@xterm/addon-fit',
      '@xterm/addon-web-links',
    ],
  },


  async rewrites() {
    // Mock mode owns /v1 in the browser, so a request escaping MSW must never reach a real backend.
    if (useMock) {
      return [];
    }
    if (isVercel) {
      if (!vercelBackendOrigin) return [];
      return [
        { source: '/mcp', destination: `${vercelBackendOrigin}/mcp/clients` },
        {
          source: '/v1/:path*',
          destination: `${vercelBackendOrigin}/v1/:path*`,
        },
      ];
    }
    // Use server service name in Docker environment, localhost for local development
    const apiHost = process.env.API_HOST || 'localhost';
    const apiPort = process.env.API_PORT || '8080';
    if (!/^[0-9]+$/.test(apiPort) || Number(apiPort) < 1 || Number(apiPort) > 65535) {
      throw new Error('API_PORT must be a valid TCP port');
    }
    return [
      { source: '/mcp', destination: `http://${apiHost}:${apiPort}/mcp/clients` },
      // Proxy the versioned backend API during local Next.js development.
      {
        source: '/v1/:path*',
        destination: `http://${apiHost}:${apiPort}/v1/:path*`,
      },
    ];
  },
};

// Force restart
export default withNextIntl(nextConfig);
