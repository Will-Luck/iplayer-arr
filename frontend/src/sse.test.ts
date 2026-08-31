import { describe, it, expect, beforeEach } from "vitest";
import { computeReconnectDelay, eventsURL } from "./sse";
import { clearApiKey, setApiKey } from "./apikey";

describe("computeReconnectDelay", () => {
  // rng = 0.5 puts the jitter at exactly 0 so we get the pure
  // exponential value.
  const noJitter = () => 0.5;

  it("exponential progression", () => {
    expect(computeReconnectDelay(1, noJitter)).toBe(1000);
    expect(computeReconnectDelay(2, noJitter)).toBe(2000);
    expect(computeReconnectDelay(3, noJitter)).toBe(4000);
    expect(computeReconnectDelay(4, noJitter)).toBe(8000);
    expect(computeReconnectDelay(5, noJitter)).toBe(16000);
  });

  it("caps at 30s", () => {
    expect(computeReconnectDelay(6, noJitter)).toBe(30000);
    expect(computeReconnectDelay(10, noJitter)).toBe(30000);
    expect(computeReconnectDelay(100, noJitter)).toBe(30000);
  });

  it("jitter envelope is +/- 25%", () => {
    // rng = 0 -> jitter = -25%
    expect(computeReconnectDelay(2, () => 0)).toBeCloseTo(2000 - 500, 0);
    // rng = 1 -> jitter = +25%
    expect(computeReconnectDelay(2, () => 1)).toBeCloseTo(2000 + 500, 0);
  });

  it("never negative", () => {
    expect(computeReconnectDelay(1, () => 0)).toBeGreaterThanOrEqual(0);
    expect(computeReconnectDelay(0, () => 0)).toBeGreaterThanOrEqual(0);
  });

  it("zero attempts treated as first attempt floor", () => {
    expect(computeReconnectDelay(0, noJitter)).toBe(1000);
  });
});

// EventSource cannot attach an Authorization header, so the SSE stream
// is the one place the credential has to ride in the query string. If
// this regresses the dashboard silently stops receiving live updates
// once /api/* is authenticated.
describe("eventsURL", () => {
  beforeEach(() => clearApiKey());

  it("is the bare path when no key is stored", () => {
    expect(eventsURL()).toBe("/api/events");
  });

  it("appends the apikey query parameter when a key is stored", () => {
    setApiKey("live-key");
    expect(eventsURL()).toBe("/api/events?apikey=live-key");
  });

  it("percent-encodes a key with URL-special characters", () => {
    setApiKey("a b&c=d");
    expect(eventsURL()).toBe("/api/events?apikey=a%20b%26c%3Dd");
  });
});
