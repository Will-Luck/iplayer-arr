import { createSignal } from "solid-js";

/**
 * localStorage slot holding the operator's API key.
 *
 * The dashboard API is authenticated, so the SPA has to carry the same
 * credential Sonarr and Radarr use. There is no login and no session
 * cookie: the key is entered once in the setup wizard and replayed on
 * every request from here. Keeping it out of a cookie is deliberate -
 * a credential the browser attaches automatically is what makes CSRF
 * possible in the first place.
 */
export const API_KEY_STORAGE_KEY = "iplayer-arr.api-key";

/** The slice of the Storage interface this module needs. */
export type KeyStore = Pick<Storage, "getItem" | "setItem" | "removeItem">;

/**
 * Returns window.localStorage, or null when it is unavailable. Access
 * throws outright in some privacy modes and in non-browser contexts such
 * as the test runner, and an unreadable key should degrade to "not set"
 * rather than take the whole app down.
 */
export function browserStore(): KeyStore | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

/** Reads and trims the persisted key. Returns "" when there is none. */
export function readStoredApiKey(store: KeyStore | null = browserStore()): string {
  if (!store) return "";
  try {
    return (store.getItem(API_KEY_STORAGE_KEY) ?? "").trim();
  } catch {
    return "";
  }
}

/**
 * Persists a trimmed key, or removes the entry entirely when the key is
 * blank. Trimming matters: a key pasted out of a terminal usually
 * carries a trailing newline, which would 401 every request with no
 * visible explanation.
 */
export function writeStoredApiKey(key: string, store: KeyStore | null = browserStore()): void {
  if (!store) return;
  const trimmed = key.trim();
  try {
    if (trimmed === "") {
      store.removeItem(API_KEY_STORAGE_KEY);
    } else {
      store.setItem(API_KEY_STORAGE_KEY, trimmed);
    }
  } catch {
    // Storage full or blocked: the in-memory signal below still holds
    // the key for this tab, so the session keeps working.
  }
}

const [apiKey, setApiKeySignal] = createSignal(readStoredApiKey());

/**
 * Reactive accessor for the current key. Components read this rather
 * than localStorage so they re-render when the wizard saves a new key.
 */
export { apiKey };

/** Persists a key and notifies every reader. */
export function setApiKey(key: string): void {
  const trimmed = key.trim();
  writeStoredApiKey(trimmed);
  setApiKeySignal(trimmed);
}

/** Forgets the key, both persisted and in memory. */
export function clearApiKey(): void {
  writeStoredApiKey("");
  setApiKeySignal("");
}

/**
 * Request headers carrying the credential. Empty when no key is stored,
 * so the request still reaches the server and comes back as a 401 the
 * app can react to, rather than being blocked client side.
 */
export function authHeaders(): Record<string, string> {
  const key = apiKey();
  return key ? { Authorization: `Bearer ${key}` } : {};
}

/**
 * Event name dispatched on window when any API call comes back 401.
 * App.tsx listens for it and reopens the setup wizard, so an operator
 * whose stored key is missing or stale gets a way back in instead of a
 * dashboard that silently renders nothing.
 */
export const UNAUTHORIZED_EVENT = "iplayer-arr:unauthorized";
