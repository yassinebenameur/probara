// Save/publish rules for the status-page template editor
// (app/status-pages/[id]/template). Kept free of React so `npm test` covers
// them (lib/template-editor.test.ts).

export interface TemplateEditorInput {
  /** Current editor text. */
  source: string;
  /**
   * What the editor's text is compared against: the stored draft when one
   * exists, otherwise the live template's source (published version or the
   * built-in). Every action that changes either must move it.
   */
  baseline: string;
  /** A draft row is stored for the page. */
  hasDraft: boolean;
}

export type TemplateEditorStatus = 'unsaved' | 'draft' | 'live';

export interface TemplateEditorState {
  /** The editor text differs from the baseline. */
  dirty: boolean;
  /**
   * Publishing promotes the stored draft, so the editor text must be saved
   * as one first whenever it is not already exactly that draft.
   */
  needsSave: boolean;
  /** unsaved: edited; draft: equals the saved draft; live: equals the live template. */
  status: TemplateEditorStatus;
}

export function templateEditorState({ source, baseline, hasDraft }: TemplateEditorInput): TemplateEditorState {
  const dirty = source !== baseline;
  return {
    dirty,
    needsSave: dirty || !hasDraft,
    status: dirty ? 'unsaved' : hasDraft ? 'draft' : 'live',
  };
}

/**
 * After an action changed what is live (reset to built-in, republish a
 * version) while no draft exists: the editor follows the new live source
 * unless it holds edits, which are kept and then read as unsaved against it.
 */
export function followLiveSource(
  current: { source: string; baseline: string },
  live: string,
): { source: string; baseline: string } {
  const dirty = current.source !== current.baseline;
  return { source: dirty ? current.source : live, baseline: live };
}
