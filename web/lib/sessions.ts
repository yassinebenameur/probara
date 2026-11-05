// Display helpers for the account sessions list. A session's user agent is
// whatever the browser sent on its latest refresh; this turns it into a
// short "Browser on OS" label and never fails on an unknown string.

const BROWSERS: Array<[RegExp, string]> = [
  [/Edg(?:e|A|iOS)?\//, 'Edge'],
  [/OPR\/|Opera/, 'Opera'],
  [/Firefox\/|FxiOS\//, 'Firefox'],
  [/Chrome\/|CriOS\//, 'Chrome'],
  [/Safari\//, 'Safari'],
  [/curl\//, 'curl'],
];

const SYSTEMS: Array<[RegExp, string]> = [
  [/iPhone|iPad|iPod/, 'iOS'],
  [/Android/, 'Android'],
  [/Windows/, 'Windows'],
  [/Mac OS X|Macintosh/, 'macOS'],
  [/CrOS/, 'ChromeOS'],
  [/Linux/, 'Linux'],
];

function match(table: Array<[RegExp, string]>, ua: string): string | null {
  for (const [pattern, name] of table) {
    if (pattern.test(ua)) return name;
  }
  return null;
}

export function describeUserAgent(ua?: string | null): string {
  const value = (ua ?? '').trim();
  if (!value) return 'Unknown device';
  const browser = match(BROWSERS, value);
  const system = match(SYSTEMS, value);
  if (browser && system) return `${browser} on ${system}`;
  if (browser || system) return (browser ?? system) as string;
  return value.length > 48 ? `${value.slice(0, 47)}…` : value;
}

// Sign-in method label for a session.
export function sessionMethodLabel(method: 'password' | 'oidc'): string {
  return method === 'oidc' ? 'SSO' : 'Password';
}
