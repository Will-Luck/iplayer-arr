export type EventHandler = (data: unknown) => void;

/**
 * Connect to the SSE endpoint and dispatch events to the provided handlers.
 * Auto-reconnects after 5 seconds on error.
 * Returns a cleanup function that closes the connection.
 */
export function connectSSE(handlers: Record<string, EventHandler>): () => void {
  let es: EventSource | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let closed = false;

  function connect() {
    if (closed) return;

    es = new EventSource("/api/events");

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
        timer = setTimeout(connect, 5000);
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
