import StatusPageBuilder from '@/components/status-pages/StatusPageBuilder';

export default function StatusPageBuilderPage() {
  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-white">Status Page Builder</h1>
          <p className="text-sm text-slate-500 mt-1">
            Design a customizable status page with live preview, layout control, and styling.
          </p>
        </div>
      </div>
      <StatusPageBuilder />
    </div>
  );
}
