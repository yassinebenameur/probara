const configuredBasePath = process.env.NEXT_PUBLIC_BASE_PATH?.trim() || '';
const basePath =
  configuredBasePath && configuredBasePath !== '/'
    ? `/${configuredBasePath.replace(/^\/+|\/+$/g, '')}`
    : '';

/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'export',
  trailingSlash: true,
  reactStrictMode: true,
  basePath,
  assetPrefix: basePath || undefined,
  images: {
    unoptimized: true,
  },
};

export default nextConfig;
