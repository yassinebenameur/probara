# Probara Web UI

Next.js-based web interface for managing monitors, alert policies, and status pages.

## Development

```bash
# Install dependencies
npm install

# Run development server
npm run dev

# Build for production
npm run build

# Start production server
npm start

# Lint code
npm run lint
```

The development server will start on `http://localhost:3000`.

## Environment Variables

- `NEXT_PUBLIC_API_URL` - Base URL for the API (defaults to `/api` for relative paths)

## Authentication

The UI uses API key authentication. Users enter their API key on the `/connect` page, which is stored in localStorage and used for all API requests.

## Project Structure

- `app/` - Next.js App Router pages
- `components/` - React components
  - `layout/` - Layout components (Sidebar, TopBar, Layout)
  - `ui/` - Reusable UI components
  - `monitors/` - Monitor-specific components
  - `alert-policies/` - Alert policy components
  - `status-pages/` - Status page components
- `lib/` - Utility functions
  - `api.ts` - API client with authentication
  - `auth.ts` - API key management
  - `types.ts` - TypeScript type definitions

## Docker

The application can be built and run using Docker:

```bash
docker build -t probara-frontend .
docker run -p 3000:3000 -e NEXT_PUBLIC_API_URL=/api probara-frontend
```

## Deployment

The application is configured for Kubernetes deployment via Helm. See the main project's Helm chart for deployment configuration.

