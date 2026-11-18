/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'standalone',
  reactStrictMode: true,
  // The default bottom-left badge sits on the sidebar's Sign out button.
  devIndicators: { position: 'bottom-right' },
  // Static browser security headers, the same values the Go services send
  // (shared/securityheaders). The per-request Content-Security-Policy comes
  // from proxy.ts. No HSTS here: this server cannot tell whether a request
  // reached it over TLS through a trusted proxy (see the docs' security page).
  async headers() {
    return [
      {
        source: '/:path*',
        headers: [
          { key: 'X-Content-Type-Options', value: 'nosniff' },
          { key: 'X-Frame-Options', value: 'DENY' },
          { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
          { key: 'Permissions-Policy', value: 'camera=(), microphone=(), geolocation=(), payment=(), usb=()' },
        ],
      },
    ];
  },
}

module.exports = nextConfig
