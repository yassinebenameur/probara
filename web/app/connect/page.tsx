'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import Button from '@/components/ui/Button';
import { setApiKey } from '@/lib/auth';

export default function ConnectPage() {
  const router = useRouter();
  const [apiKey, setApiKeyValue] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);

    if (!apiKey.trim()) {
      setError('API key is required');
      setLoading(false);
      return;
    }

    // Test the API key by making a simple request
    try {
      const response = await fetch('/api/v1/monitors?page_size=1', {
        headers: {
          Authorization: `Bearer ${apiKey.trim()}`,
        },
      });

      if (response.status === 401) {
        setError('Invalid API key. Please check your key and try again.');
        setLoading(false);
        return;
      }

      if (!response.ok) {
        throw new Error('Failed to validate API key');
      }

      // Save the API key
      setApiKey(apiKey.trim());
      
      // Redirect to dashboard
      router.push('/');
    } catch (err) {
      setError('Failed to connect. Please check your API key and try again.');
      setLoading(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center">
      {/* Background gradient */}
      <div className="pointer-events-none fixed inset-0 bg-gradient-to-br from-cyan-500/5 via-transparent to-violet-500/5" />
      
      <div className="relative w-full max-w-md p-8">
        {/* Card */}
        <div className="rounded-2xl border border-white/[0.08] bg-slate-900/60 p-8 backdrop-blur-xl">
          {/* Logo */}
          <div className="mb-8 flex justify-center">
            <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-gradient-to-br from-cyan-400 to-violet-500 shadow-lg shadow-cyan-500/20">
              <svg className="h-8 w-8 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" />
              </svg>
            </div>
          </div>

          {/* Header */}
          <div className="mb-8 text-center">
            <h1 className="text-2xl font-semibold tracking-tight text-white">
              Welcome Back
            </h1>
            <p className="mt-2 text-sm text-slate-500">
              Enter your API key to access the dashboard
            </p>
          </div>

          {/* Form */}
          <form onSubmit={handleSubmit} className="space-y-6">
            <div>
              <label htmlFor="api-key" className="mb-2 block text-sm font-medium text-slate-400">
                API Key
              </label>
              <input
                id="api-key"
                name="api-key"
                type="password"
                required
                className="input"
                placeholder="Enter your API key"
                value={apiKey}
                onChange={(e) => setApiKeyValue(e.target.value)}
                disabled={loading}
              />
            </div>

            {error && (
              <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3">
                <p className="text-sm text-rose-400">{error}</p>
              </div>
            )}

            <Button
              type="submit"
              variant="accent"
              size="sm"
              loading={loading}
              disabled={loading}
              className="w-full justify-center"
            >
              {loading ? 'Connecting…' : 'Continue to dashboard'}
            </Button>
          </form>

          {/* Footer */}
          <div className="mt-8 text-center">
            <p className="text-xs text-slate-600">
              Need an API key? Check the{' '}
              <a href="#" className="text-cyan-500 hover:text-cyan-400">
                documentation
              </a>
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
