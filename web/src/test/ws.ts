import { Server } from "mock-socket";
import type {
  ImportSocketMessage,
  ServerLogSocketMessage,
} from "@/types/api";

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

// The base URL useLogsSocket builds (without query params — mock-socket matches
// on the path, and the hook appends ?token=&afterId=).
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
