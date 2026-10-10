/**
 * The instrument field every glass surface sits on: soft light sources, a
 * faint grid, and an alarm bloom that fades in while html[data-health] is
 * 'alert' (set by useFleetHealth). Styling lives with the surface rules at
 * the top of app/globals.css.
 */
export default function AmbientBackdrop() {
  return (
    <div className="ambient" aria-hidden="true">
      <div className="ambient-light" />
      <div className="ambient-grid" />
      <div className="ambient-alarm" />
    </div>
  );
}
