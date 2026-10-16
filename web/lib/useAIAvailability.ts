'use client';

import { useEffect, useState } from 'react';

import { getAISettings } from '@/lib/api';
import { watchAIAvailability } from '@/lib/ai-availability';
import type { AISettings } from '@/lib/types';

/**
 * AI settings for gating an AI trigger: null until the first read (and after
 * a failed one), then refreshed while AI is off or the local service is not
 * ready, so the trigger re-enables once it is. See watchAIAvailability.
 */
export function useAIAvailability(): AISettings | null {
  const [availability, setAvailability] = useState<AISettings | null>(null);
  useEffect(() => {
    const watch = watchAIAvailability(getAISettings, setAvailability, {
      isHidden: () => document.visibilityState === 'hidden',
    });
    const onFocus = () => watch.recheck();
    window.addEventListener('focus', onFocus);
    return () => {
      window.removeEventListener('focus', onFocus);
      watch.stop();
    };
  }, []);
  return availability;
}
