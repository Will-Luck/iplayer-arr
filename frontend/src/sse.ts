export type EventHandler = (data: unknown) => void;

/**
 * Base delay before the first reconnect attempt. Doubles per attempt
 * up to RECONNECT_CAP_MS. Audit item 28.
 */
const RECONNECT_BASE_MS = 1000;

/**
 * Upper bound on the exponential backoff. After ~5 failures the delay
 * sits at this value; further attempts add only the jitter component.
 */
const RECONNECT_CAP_MS = 30_000;

/**
 * Symmetric jitter as a fraction of the current exponential delay.
 * 0.25 means the delivered delay lands within +/- 25% of the base,
 * spreading a fleet of clients across the reconnect window so a
 * server restart does not see a synchronised thundering herd.
 */
const RECONNECT_JITTER = 0.25;

/**
 * computeReconnectDelay derives the next reconnect delay for an
 * EventSource that has just failed. `attempts` is the post-increment
 * failure count (1 for the first failure, 2 for the second, etc).
 * `rng` is injectable so the test suite can verify the jitter envelope
 * without flake.
 */
export function computeReconnectDelay(attempts: number, rng: () => number = Math.random): number {
  const exp = Math.min(RECONNECT_CAP_MS, RECONNECT_BASE_MS * Math.pow(2, Math.max(0, attempts - 1)));
  const jitter = exp * RECONNECT_JITTER * (rng() * 2 - 1);
  return Math.max(0, exp + jitter);
}

/**
 * Connect to the SSE endpoint and dispatch events to the provided
 * handlers. Auto-reconnects with exponential backoff (1 s base,
 * doubling to a 30 s cap) plus +/- 25 % jitter so a fleet of clients
 * does not synchronise reconnect storms after a server restart. The
 * attempt counter is reset on a successful open. Audit item 28.
 *
 * Returns a cleanup function that closes the connection and clears
 * any pending reconnect timer.
 */
export function connectSSE(handlers: Record<string, EventHandler>): () => void {
  let es: EventSource | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let closed = false;
  let attempts = 0;

  function connect() {
    if (closed) return;

    es = new EventSource("/api/events");

    es.addEventListener("open", () => {
      attempts = 0;
    });

    for (const [eventType, handler] of Object.entries(handlers)) {
      es.addEventListener(eventType, (e: MessageEvent) => {
        try {
          const data = JSON.parse(e.data);
          handler(data);
        } catch {
          // ignore malformed data
        }
      });
    }

    es.onerror = () => {
      es?.close();
      es = null;
      if (!closed) {
        attempts++;
        timer = setTimeout(connect, computeReconnectDelay(attempts));
      }
    };
  }

  connect();

  return () => {
    closed = true;
    if (timer) clearTimeout(timer);
    es?.close();
    es = null;
  };
}
