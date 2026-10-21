/**
 * Shared classes for sidebar rows that fold into the collapsed icon rail.
 *
 * A row keeps one DOM structure in both states, so nothing remounts and
 * nothing reflows mid-animation: the row clips (overflow-hidden,
 * whitespace-nowrap), its padding eases from the expanded 32px row to the
 * 40×40 rail target, and the label fades. Collapsing fades the label out at
 * once; expanding fades it in a beat late, once the width has mostly opened.
 * Padding follows the sidebar's own width motion (slow, emphasized); hover
 * colours keep the fast standard curve. Reduced motion collapses all of it
 * (app/globals.css).
 */
export const RAIL_ROW_MOTION =
  '[transition:padding_var(--dur-slow)_var(--ease-emphasized),background-color_var(--dur-fast)_var(--ease-standard),color_var(--dur-fast)_var(--ease-standard),box-shadow_var(--dur-fast)_var(--ease-standard)]';

/**
 * Row padding: the expanded 32px row, or a 40×40 square around a 16px icon
 * (11px + 18px line + 11px). Rows sit in a railGutter container.
 */
export function railRowPadding(rail: boolean) {
  return rail ? 'px-3 py-[11px]' : 'px-2.5 py-[7px]';
}

/**
 * Horizontal padding of the containers that hold rows. The rail is 64px with
 * a 1px edge each side, so 11px leaves exactly the 40px row, centred.
 */
export function railGutter(rail: boolean) {
  return rail ? 'px-[11px]' : 'px-3';
}

/** The row's text: still the accessible name in the rail, just not painted. */
export function railLabel(rail: boolean) {
  return `min-w-0 truncate transition-opacity ${
    rail ? 'opacity-0 duration-fast' : 'opacity-100 duration-base delay-[var(--dur-fast)]'
  }`;
}
