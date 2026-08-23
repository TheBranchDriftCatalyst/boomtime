import { Server } from "mock-socket";
import type {
  ImportSocketMessage,
  ServerLogSocketMessage,
} from "@shared/types/api";

// The URL useImportJobSocket builds from window.location. In vitest/jsdom the
// origin is http://localhost:3000, so the socket connects to
// ws://localhost:3000/import/jobs/:id/ws.
export function importWsUrl(jobId: number): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/import/jobs/${jobId}/ws`;
}

export interface MockImportWs {
  server: Server;
  /** Send a typed message to the connected client. */
  send: (msg: ImportSocketMessage) => void;
  /** Send raw (e.g. malformed) text. */
  sendRaw: (data: string) => void;
  /** Close the current client socket (triggers reconnect logic). */
  closeClient: () => void;
  /** Resolves when a client connects (or immediately if already connected). */
  connected: () => Promise<void>;
  stop: () => void;
}

/**
 * Installs a mock-socket server for the import WS and swaps the global
 * WebSocket so the hook connects to it. Returns helpers to drive server→client
 * messages and to observe/force connection lifecycle. Caller must `stop()`.
 */
export function mockImportWs(jobId: number): MockImportWs {
  const url = importWsUrl(jobId);
  const realWebSocket = globalThis.WebSocket;

  // jsdom defines WebSocket as a non-writable property; make it configurable so
  // mock-socket's Server can swap in its own WebSocket during construction.
  Object.defineProperty(globalThis, "WebSocket", {
    value: realWebSocket,
    writable: true,
    configurable: true,
  });

  // Constructing the Server swaps globalThis.WebSocket to mock-socket's client,
  // which intercepts connections to the mock server URL.
  const server = new Server(url);

  let socket: import("mock-socket").Client | null = null;
  let connectResolve: (() => void) | null = null;

  server.on("connection", (s) => {
    socket = s;
    connectResolve?.();
    connectResolve = null;
  });

  return {
    server,
    send(msg) {
      socket?.send(JSON.stringify(msg));
    },
    sendRaw(data) {
      socket?.send(data);
    },
    closeClient() {
      socket?.close();
      socket = null;
    },
    connected() {
      if (socket) return Promise.resolve();
      return new Promise<void>((resolve) => {
        connectResolve = resolve;
      });
    },
    stop() {
      server.stop();
      Object.defineProperty(globalThis, "WebSocket", {
        value: realWebSocket,
        writable: true,
        configurable: true,
      });
    },
  };
}

// --- CLI runner stream (boom-hney.5) ----------------------------------------

export function cliRunWsUrl(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/api/v1/admin/cli/run/ws`;
}

export interface MockCliRunWs {
  server: Server;
  /** Resolves with the parsed run-request payload once the client sends it. */
  requestReceived: () => Promise<Record<string, unknown>>;
  /** Stream a frame (start/output/done/error) back to the client. */
  send: (frame: Record<string, unknown>) => void;
  stop: () => void;
}

/**
 * Installs a mock-socket server for the CLI run stream and swaps the global
 * WebSocket. Captures the single run-request frame the client sends (for
 * payload assertions) and lets the test stream output/done frames back.
 */
export function mockCliRunWs(): MockCliRunWs {
  const url = cliRunWsUrl();
  const realWebSocket = globalThis.WebSocket;
  Object.defineProperty(globalThis, "WebSocket", {
    value: realWebSocket,
    writable: true,
    configurable: true,
  });

  const server = new Server(url);
  let socket: import("mock-socket").Client | null = null;
  let req: Record<string, unknown> | undefined;
  let reqResolve: ((v: Record<string, unknown>) => void) | null = null;

  server.on("connection", (s) => {
    socket = s;
    s.on("message", (data) => {
      try {
        req = JSON.parse(String(data)) as Record<string, unknown>;
      } catch {
        req = {};
      }
      reqResolve?.(req);
      reqResolve = null;
    });
  });

  return {
    server,
    requestReceived() {
      return req !== undefined
        ? Promise.resolve(req)
        : new Promise((resolve) => {
            reqResolve = resolve;
          });
    },
    send(frame) {
      socket?.send(JSON.stringify(frame));
    },
    stop() {
      server.stop();
      Object.defineProperty(globalThis, "WebSocket", {
        value: realWebSocket,
        writable: true,
        configurable: true,
      });
    },
  };
}

// The base URL useLogsSocket builds (without query params — mock-socket matches
// on the path, and the hook may append ?afterId=).
export function serverLogsWsUrl(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/api/v1/logs/ws`;
}

export interface MockLogsWs {
  server: Server;
  /** Send a typed server-log message to the connected client. */
  send: (msg: ServerLogSocketMessage) => void;
  /** Send raw (e.g. malformed) text. */
  sendRaw: (data: string) => void;
  /** Close the current client socket (triggers reconnect logic). */
  closeClient: () => void;
  /** The query string the last client connected with (for asserting afterId). */
  lastUrl: () => string | undefined;
  /** Resolves when a client connects (or immediately if already connected). */
  connected: () => Promise<void>;
  stop: () => void;
}

/**
 * Installs a mock-socket server for the server-logs WS and swaps the global
 * WebSocket. Mirrors mockImportWs.
 */
export function mockLogsWs(): MockLogsWs {
  const url = serverLogsWsUrl();
  const realWebSocket = globalThis.WebSocket;

  Object.defineProperty(globalThis, "WebSocket", {
    value: realWebSocket,
    writable: true,
    configurable: true,
  });

  const server = new Server(url);

  let socket: import("mock-socket").Client | null = null;
  let connectResolve: (() => void) | null = null;
  let lastUrl: string | undefined;

  server.on("connection", (s) => {
    socket = s;
    lastUrl = s.url;
    connectResolve?.();
    connectResolve = null;
  });

  return {
    server,
    send(msg) {
      socket?.send(JSON.stringify(msg));
    },
    sendRaw(data) {
      socket?.send(data);
    },
    closeClient() {
      socket?.close();
      socket = null;
    },
    lastUrl() {
      return lastUrl;
    },
    connected() {
      if (socket) return Promise.resolve();
      return new Promise<void>((resolve) => {
        connectResolve = resolve;
      });
    },
    stop() {
      server.stop();
      Object.defineProperty(globalThis, "WebSocket", {
        value: realWebSocket,
        writable: true,
        configurable: true,
      });
    },
  };
}
