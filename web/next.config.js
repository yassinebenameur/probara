/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'standalone',
  reactStrictMode: true,
  // The default bottom-left badge sits on the sidebar's Sign out button.
  devIndicators: { position: 'bottom-right' },
}

module.exports = nextConfig
