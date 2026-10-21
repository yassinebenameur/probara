// Settings layout. The page uses the full data width, so panels flow into
// equal columns that are at least 32rem wide and at most two per row. The
// column count comes from the container width (auto-fit), not viewport
// breakpoints, because the collapsible sidebar changes the main width at the
// same viewport. auto-fit collapses empty tracks, so a tab with one panel
// fills the row instead of leaving an empty column. Two columns start at a
// 1048px main column, so the common laptop widths (1440 with the sidebar
// expanded, 1280 with it collapsed) already get two. Use `col-span-full` for content that needs the whole row; a
// fixed `col-span-2` would add an implicit track when only one fits.
export const SETTINGS_COLUMNS =
  'grid gap-6 grid-cols-[repeat(auto-fit,minmax(min(100%,max(32rem,calc((100%_-_1.5rem)/2))),1fr))]';

// The same flow for panels whose forms need a wider column before they sit
// side by side (Access: the API-key form and the group-mapping form). Two
// columns start at a 1432px main column.
export const SETTINGS_COLUMNS_WIDE =
  'grid gap-6 grid-cols-[repeat(auto-fit,minmax(min(100%,max(44rem,calc((100%_-_1.5rem)/2))),1fr))]';

// Notifications: its section cards sit in explicit column stacks
// (NotificationsPanel), not grid cells, so a short card never leaves a hole
// beside a tall one and each card is only as wide as its column. The count
// comes from the panel's own width (it moves with the sidebar): three
// columns once each gets 35rem, two once each gets 26rem, one below that,
// so no card is ever wide enough to strand a short row's text far from its
// control. Columns under 540px are `stacked`: a parameter row puts its
// segmented control under its text, stretched to the row width, because
// beside the text it would squeeze the description to a few words.
export const SECTION_GAP_PX = 24;
const STACKED_BELOW_PX = 540;
export interface SectionLayout {
  columns: 1 | 2 | 3;
  stacked: boolean;
}
export function sectionLayout(width: number): SectionLayout {
  const columns: 1 | 2 | 3 =
    width >= 3 * 560 + 2 * SECTION_GAP_PX ? 3 : width >= 2 * 416 + SECTION_GAP_PX ? 2 : 1;
  const columnWidth = (width - (columns - 1) * SECTION_GAP_PX) / columns;
  return { columns, stacked: columnWidth < STACKED_BELOW_PX };
}

// The same flow inside one wide panel (AI): two sections side
// by side once each gets 26rem, so a full-width panel never stretches one
// column of controls across the screen.
export const SETTINGS_PANEL_COLUMNS =
  'grid items-start gap-x-8 gap-y-6 grid-cols-[repeat(auto-fit,minmax(min(100%,max(26rem,calc((100%_-_2rem)/2))),1fr))]';
