/** Continue pending analyses across transient fetch failures and empty replies. */
export function pollAnalysis<T extends { status: string }>(
  read: () => Promise<T | null>,
  onResult: (value: T) => void,
  onFailure: () => void,
  interval: number,
  schedule: (callback: () => void, delay: number) => ReturnType<typeof setTimeout> = setTimeout,
  unschedule: (timer: ReturnType<typeof setTimeout>) => void = clearTimeout,
): () => void {
  let stopped = false;
  let timer: ReturnType<typeof setTimeout>;
  const poll = async () => {
    let pending = true;
    try {
      const latest = await read();
      if (!stopped && latest) {
        onResult(latest);
        pending = latest.status === 'pending';
      }
    } catch {
      if (!stopped) onFailure();
    } finally {
      if (!stopped && pending) timer = schedule(() => { void poll(); }, interval);
    }
  };
  timer = schedule(() => { void poll(); }, interval);
  return () => { stopped = true; unschedule(timer); };
}
