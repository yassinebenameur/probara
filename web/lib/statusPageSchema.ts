export type StatusPageDevice = 'desktop' | 'tablet' | 'mobile';

export type StatusPageBlockType =
  | 'hero'
  | 'status'
  | 'incidents'
  | 'metrics'
  | 'updates'
  | 'cta'
  | 'custom';

export interface StatusPageBlockStyle {
  backgroundColor?: string;
  textColor?: string;
  borderColor?: string;
  borderRadius?: number;
  padding?: number;
  align?: 'left' | 'center' | 'right';
}

export interface StatusPageBlock {
  id: string;
  type: StatusPageBlockType;
  title: string;
  content: string;
  visible: boolean;
  style: StatusPageBlockStyle;
}

export interface StatusPageGlobalStyle {
  pageBackground: string;
  textColor: string;
  accentColor: string;
  fontFamily: string;
  baseFontSize: number;
  headingFontSize: number;
  sectionSpacing: number;
  borderRadius: number;
  borderColor: string;
}

export interface StatusPageLogo {
  url?: string;
  placement: 'left' | 'center' | 'right';
  visible: boolean;
}

export interface StatusPageLayout {
  blocks: StatusPageBlock[];
  logo: StatusPageLogo;
}

export interface StatusPageVersion {
  name: string;
  updatedAt: string;
  layout: StatusPageLayout;
  styles: StatusPageGlobalStyle;
}

export interface StatusPageDocument {
  version: string;
  draft: StatusPageVersion;
  published: StatusPageVersion;
}

export const statusPageSchema = {
  $schema: 'http://json-schema.org/draft-07/schema#',
  title: 'Status Page Layout + Styles',
  type: 'object',
  required: ['version', 'draft', 'published'],
  properties: {
    version: { type: 'string' },
    draft: { $ref: '#/$defs/version' },
    published: { $ref: '#/$defs/version' },
  },
  $defs: {
    version: {
      type: 'object',
      required: ['name', 'updatedAt', 'layout', 'styles'],
      properties: {
        name: { type: 'string' },
        updatedAt: { type: 'string' },
        layout: { $ref: '#/$defs/layout' },
        styles: { $ref: '#/$defs/globalStyles' },
      },
    },
    layout: {
      type: 'object',
      required: ['blocks', 'logo'],
      properties: {
        blocks: {
          type: 'array',
          items: { $ref: '#/$defs/block' },
        },
        logo: { $ref: '#/$defs/logo' },
      },
    },
    logo: {
      type: 'object',
      required: ['placement', 'visible'],
      properties: {
        url: { type: 'string' },
        placement: { enum: ['left', 'center', 'right'] },
        visible: { type: 'boolean' },
      },
    },
    globalStyles: {
      type: 'object',
      required: [
        'pageBackground',
        'textColor',
        'accentColor',
        'fontFamily',
        'baseFontSize',
        'headingFontSize',
        'sectionSpacing',
        'borderRadius',
        'borderColor',
      ],
      properties: {
        pageBackground: { type: 'string' },
        textColor: { type: 'string' },
        accentColor: { type: 'string' },
        fontFamily: { type: 'string' },
        baseFontSize: { type: 'number' },
        headingFontSize: { type: 'number' },
        sectionSpacing: { type: 'number' },
        borderRadius: { type: 'number' },
        borderColor: { type: 'string' },
      },
    },
    block: {
      type: 'object',
      required: ['id', 'type', 'title', 'content', 'visible', 'style'],
      properties: {
        id: { type: 'string' },
        type: {
          enum: ['hero', 'status', 'incidents', 'metrics', 'updates', 'cta', 'custom'],
        },
        title: { type: 'string' },
        content: { type: 'string' },
        visible: { type: 'boolean' },
        style: { $ref: '#/$defs/blockStyles' },
      },
    },
    blockStyles: {
      type: 'object',
      properties: {
        backgroundColor: { type: 'string' },
        textColor: { type: 'string' },
        borderColor: { type: 'string' },
        borderRadius: { type: 'number' },
        padding: { type: 'number' },
        align: { enum: ['left', 'center', 'right'] },
      },
    },
  },
} as const;
