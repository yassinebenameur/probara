export type DocTable = {
  type: 'table';
  columns: string[];
  rows: string[][];
};

export type DocParagraph = {
  type: 'paragraph';
  text: string;
};

export type DocList = {
  type: 'list';
  ordered?: boolean;
  items: string[];
};

export type DocCode = {
  type: 'code';
  code: string;
  language?: string;
  title?: string;
};

export type DocCallout = {
  type: 'callout';
  tone?: 'info' | 'warning' | 'success';
  title: string;
  text: string;
};

export type DocDefinitionList = {
  type: 'definitions';
  items: Array<{
    term: string;
    description: string;
  }>;
};

export type DocBlocks =
  | DocTable
  | DocParagraph
  | DocList
  | DocCode
  | DocCallout
  | DocDefinitionList;

export type DocSection = {
  id: string;
  title: string;
  intro?: string;
  blocks: DocBlocks[];
};

export type DocPage = {
  slug: string;
  group: string;
  title: string;
  description: string;
  eyebrow: string;
  readingTime: string;
  keywords: string[];
  sections: DocSection[];
};

export type DocNavGroup = {
  title: string;
  items: Array<{
    slug: string;
    title: string;
    description: string;
  }>;
};
