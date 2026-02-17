/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'standalone',
  reactStrictMode: true,
  outputFileTracingIncludes: {
    '/': ['./node_modules/**/*'],
  },
}

module.exports = nextConfig
