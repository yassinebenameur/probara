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

// The same flow inside one wide panel (Notifications, AI): two sections side
// by side once each gets 26rem, so a full-width panel never stretches one
// column of controls across the screen.
export const SETTINGS_PANEL_COLUMNS =
  'grid items-start gap-x-8 gap-y-6 grid-cols-[repeat(auto-fit,minmax(min(100%,max(26rem,calc((100%_-_2rem)/2))),1fr))]';
