// useAvatarPromptStream (gaka-9v4) — a small hook that reads the
// OpenAI-compat SSE stream from
//   POST /api/v1/admin/avatar/synthesize-prompt
// and appends every delta.content chunk to an accumulating `text` string.
//
// The server proxies the upstream 1:1 so the wire format is exactly the
// OpenAI Chat Completions streaming shape:
//
//   data: {"choices":[{"delta":{"content":"chibi"}}]}
//   data: {"choices":[{"delta":{"content":" portrait"}}]}
//   ...
//   data: [DONE]
//
// We deliberately DON'T use catalyst-ui's useStreamingChat here because its
// LLMProvider is opinionated about routing everything through a LiteLLM
// client — we want the server-side key path, not a browser-side call.
// Cost of that decision: ~40 lines of hand-rolled SSE parsing (below).
// Benefit: zero LLM API key ever ships to the browser.
import { useCallback, useRef, useState } from "react";
import { authStore } from "@/features/auth/auth";

export interface AvatarSynthInput {
  topLabels: string[];
  synopsis: string;
}

export interface UseAvatarPromptStreamResult {
  /** The accumulating streamed prompt text. */
  text: string;
  /** True while the stream is open. */
  isStreaming: boolean;
  /** Non-null when the last synthesize() call failed. */
  error: string | null;
  /** Kick off a new stream. Cancels any prior in-flight stream. */
  synthesize: (input: AvatarSynthInput) => Promise<void>;
  /** Abort the current stream (if any). Idempotent. */
  abort: () => void;
  /** Directly set the text (so the user can edit it before RENDER). */
  setText: (t: string) => void;
}

// parseSSELine: given one "data: ..." line, return the extracted delta
// content or null (heartbeat, keepalive, [DONE], malformed). Never
// throws — a hostile upstream shouldn't crash the FE.
function parseSSELine(line: string): string | null {
  if (!line.startsWith("data:")) return null;
  const payload = line.slice(5).trim();
  if (payload === "" || payload === "[DONE]") return null;
  try {
    const obj = JSON.parse(payload) as {
      choices?: Array<{ delta?: { content?: string } }>;
    };
    const delta = obj.choices?.[0]?.delta?.content;
    return typeof delta === "string" ? delta : null;
  } catch {
    return null;
  }
}

export function useAvatarPromptStream(): UseAvatarPromptStreamResult {
  const [text, setText] = useState("");
  const [isStreaming, setIsStreaming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // We keep the abort controller in a ref (not state) so a concurrent
  // synthesize() call can tear down the prior stream without triggering
  // a re-render just for the swap.
  const abortRef = useRef<AbortController | null>(null);

  const abort = useCallback(() => {
    if (abortRef.current) {
      abortRef.current.abort();
      abortRef.current = null;
    }
    setIsStreaming(false);
  }, []);

  const synthesize = useCallback(async (input: AvatarSynthInput) => {
    abort(); // tear down any prior stream
    setText("");
    setError(null);
    setIsStreaming(true);
    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const headers: Record<string, string> = {
        "Content-Type": "application/json",
        Accept: "text/event-stream",
      };
      const h = authStore.authHeader();
      if (h) headers.Authorization = h;

      const res = await fetch("/api/v1/admin/avatar/synthesize-prompt", {
        method: "POST",
        headers,
        body: JSON.stringify(input),
        signal: controller.signal,
        credentials: "include",
      });
      if (!res.ok) {
        // Try to surface the server's error message (503 "LLM not
        // configured", 400 bad body, 502 upstream fail).
        let msg = `stream failed: ${res.status}`;
        try {
          const j = (await res.json()) as { message?: string; error?: string };
          msg = j.message || j.error || msg;
        } catch {
          // fall through with the status-line fallback
        }
        setError(msg);
        setIsStreaming(false);
        abortRef.current = null;
        return;
      }
      if (!res.body) {
        setError("stream: empty response body");
        setIsStreaming(false);
        abortRef.current = null;
        return;
      }

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      // Read chunk-by-chunk. SSE frames are separated by `\n\n`, but the
      // server proxy emits one `\n` per line so we split on single `\n`
      // and parse each `data:` line individually. Both shapes work because
      // parseSSELine handles empty / non-data lines by returning null.
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        // The last partial line stays in buffer for the next chunk.
        buffer = lines.pop() ?? "";
        for (const line of lines) {
          const delta = parseSSELine(line);
          if (delta) setText((t) => t + delta);
        }
      }
      // Flush the trailing buffer (may contain a final data: line
      // without a newline suffix).
      if (buffer.length > 0) {
        const delta = parseSSELine(buffer);
        if (delta) setText((t) => t + delta);
      }
    } catch (err) {
      // AbortError is expected on user-initiated cancel — treat as clean end.
      if ((err as { name?: string })?.name !== "AbortError") {
        setError((err as Error).message || "stream error");
      }
    } finally {
      setIsStreaming(false);
      abortRef.current = null;
    }
  }, [abort]);

  return { text, isStreaming, error, synthesize, abort, setText };
}
