'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import Button from '@/components/ui/Button';
import BrandMark from '@/components/ui/BrandMark';
import Callout from '@/components/ui/Callout';
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
    <div className="flex min-h-screen items-center justify-center px-4 py-10">
      <div className="glass-overlay w-full max-w-md p-8">
        <div className="mb-6 flex justify-center">
          <BrandMark size={48} />
        </div>

        <div className="mb-8 text-center">
          <h1 className="text-2xl font-semibold tracking-tight text-white">
            Welcome back
          </h1>
          <p className="mt-2 text-sm text-slate-400">
            Enter your API key to access the dashboard
          </p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-5">
          <div>
            <label htmlFor="api-key" className="mb-1.5 block text-[13px] font-medium text-slate-300">
              API key
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

          {error && <Callout tone="danger">{error}</Callout>}

          <Button
            type="submit"
            variant="accent"
            size="sm"
            loading={loading}
            disabled={loading}
            className="!h-10 w-full justify-center !text-sm"
          >
            {loading ? 'Connecting…' : 'Continue to dashboard'}
          </Button>
        </form>

        <p className="mt-8 text-center text-xs text-slate-500">
          Need an API key? Check the{' '}
          <a href="#" className="text-cyan-300 hover:text-cyan-200">
            documentation
          </a>
        </p>
      </div>
    </div>
  );
}
