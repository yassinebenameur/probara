export type StoredApiKey = {
  id: string;
  name: string;
  key: string;
  key_prefix?: string;
  created_at?: string;
};

const API_KEYS_STORAGE_KEY = 'probara_api_keys';

export function loadStoredApiKeys(): StoredApiKey[] {
  if (typeof window === 'undefined') {
    return [];
  }

  try {
    const raw = localStorage.getItem(API_KEYS_STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((item) => item && typeof item.id === 'string' && typeof item.key === 'string');
  } catch {
    return [];
  }
}

export function saveStoredApiKey(entry: StoredApiKey): void {
  if (typeof window === 'undefined') {
    return;
  }

  const keys = loadStoredApiKeys();
  const index = keys.findIndex((item) => item.id === entry.id);
  if (index >= 0) {
    keys[index] = { ...keys[index], ...entry };
  } else {
    keys.unshift(entry);
  }
  localStorage.setItem(API_KEYS_STORAGE_KEY, JSON.stringify(keys));
}

export function removeStoredApiKey(id: string): void {
  if (typeof window === 'undefined') {
    return;
  }

  const keys = loadStoredApiKeys().filter((item) => item.id !== id);
  localStorage.setItem(API_KEYS_STORAGE_KEY, JSON.stringify(keys));
}
