/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'standalone',
  reactStrictMode: true,
  // The default bottom-left badge sits on the sidebar's Sign out button.
  devIndicators: { position: 'bottom-right' },
  // Static browser security headers. The per-request Content-Security-Policy
  // (nonce, report-only until WEB_CSP_ENFORCE=true) and HSTS come from
  // proxy.ts.
  async headers() {
    return [
      {
        source: '/:path*',
        headers: [
          { key: 'X-Content-Type-Options', value: 'nosniff' },
          { key: 'X-Frame-Options', value: 'DENY' },
          { key: 'Referrer-Policy', value: 'no-referrer' },
          { key: 'Permissions-Policy', value: 'camera=(), microphone=(), geolocation=()' },
        ],
      },
    ];
  },
}

module.exports = nextConfig
