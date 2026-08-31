import { describe, it, expect, beforeEach } from "vitest";
import {
  API_KEY_STORAGE_KEY,
  apiKey,
  authHeaders,
  clearApiKey,
  readStoredApiKey,
  setApiKey,
  writeStoredApiKey,
  type KeyStore,
} from "./apikey";

function memoryStore(seed: Record<string, string> = {}): KeyStore & { data: Record<string, string> } {
  const data = { ...seed };
  return {
    data,
    getItem: (k: string) => (k in data ? data[k] : null),
    setItem: (k: string, v: string) => {
      data[k] = v;
    },
    removeItem: (k: string) => {
      delete data[k];
    },
  };
}

describe("readStoredApiKey", () => {
  it("returns the stored value", () => {
    const store = memoryStore({ [API_KEY_STORAGE_KEY]: "abc123" });
    expect(readStoredApiKey(store)).toBe("abc123");
  });

  it("returns an empty string when nothing is stored", () => {
    expect(readStoredApiKey(memoryStore())).toBe("");
  });

  // A user pasting from a terminal routinely brings a trailing newline
  // along; that would 401 on every request with no visible cause.
  it("trims surrounding whitespace", () => {
    const store = memoryStore({ [API_KEY_STORAGE_KEY]: "  abc123\n" });
    expect(readStoredApiKey(store)).toBe("abc123");
  });

  it("survives a storage backend that throws", () => {
    const hostile: KeyStore = {
      getItem: () => {
        throw new Error("SecurityError");
      },
      setItem: () => {},
      removeItem: () => {},
    };
    expect(readStoredApiKey(hostile)).toBe("");
  });
});

describe("writeStoredApiKey", () => {
  it("persists a trimmed key", () => {
    const store = memoryStore();
    writeStoredApiKey(" abc123 \n", store);
    expect(store.data[API_KEY_STORAGE_KEY]).toBe("abc123");
  });

  it("removes the entry when given a blank key", () => {
    const store = memoryStore({ [API_KEY_STORAGE_KEY]: "abc123" });
    writeStoredApiKey("   ", store);
    expect(API_KEY_STORAGE_KEY in store.data).toBe(false);
  });
});

describe("apiKey signal", () => {
  beforeEach(() => clearApiKey());

  it("starts empty and follows setApiKey", () => {
    expect(apiKey()).toBe("");
    setApiKey("live-key");
    expect(apiKey()).toBe("live-key");
    clearApiKey();
    expect(apiKey()).toBe("");
  });
});

describe("authHeaders", () => {
  beforeEach(() => clearApiKey());

  it("is empty with no key so the request still reaches the server", () => {
    expect(authHeaders()).toEqual({});
  });

  it("carries a Bearer token once a key is set", () => {
    setApiKey("live-key");
    expect(authHeaders()).toEqual({ Authorization: "Bearer live-key" });
  });
});
