import { Activity } from 'lucide-react';

export function BrandMark({ compact = false }: { compact?: boolean }) {
  return (
    <span className="brand-lockup">
      <span className="brand-mark" aria-hidden="true">
        <Activity size={18} strokeWidth={2.4} />
      </span>
      {!compact && (
        <span className="brand-word">
          Probara
          <span className="brand-dot">.</span>
        </span>
      )}
    </span>
  );
}
