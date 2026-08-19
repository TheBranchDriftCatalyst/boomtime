import {
  createContext,
  useContext,
  useState,
  useCallback,
  useEffect,
  type ReactNode,
} from "react";
import { isDevUtilsEnabled, isBackendSyncEnabled } from "../utils/devMode";

/**
 * Annotation type - represents a user-created annotation
 */
export interface Annotation {
  /** Unique identifier (UUID) */
  id: string;
  /** Component name (user-typed, no React Fiber introspection) */
  componentName: string;
  /** Annotation note/content */
  note: string;
  /** Annotation type */
  type: "todo" | "bug" | "note" | "docs";
  /** Priority level */
  priority: "low" | "medium" | "high";
  /** Creation timestamp (milliseconds since epoch) */
  timestamp: number;
  /** Optional: File path (for instance-scoped annotations - legacy) */
  filePath?: string;
  /** Optional: Line number (for instance-scoped annotations - legacy) */
  lineNumber?: number;
  /** Optional: Instance identifier (for instance-scoped annotations) */
  instanceId?: string;
  /** Optional: Tree path (for instance-scoped annotations) */
  treePath?: string;
}

interface AnnotationContextValue {
  /**
   * Get all annotations
   */
  getAllAnnotations: () => Annotation[];

  /**
   * Get annotations for a specific component
   * @param componentName - The component name to filter by
   */
  getAnnotationsByComponent: (componentName: string) => Annotation[];

  /**
   * Add a new annotation
   * @param annotation - Annotation data (id and timestamp will be auto-generated if not provided)
   */
  addAnnotation: (
    annotation: Omit<Annotation, "id" | "timestamp"> & Partial<Pick<Annotation, "id" | "timestamp">>
  ) => void;

  /**
   * Remove an annotation by ID
   * @param id - The annotation ID to remove
   */
  removeAnnotation: (id: string) => void;

  /**
   * Update an existing annotation
   * @param id - The annotation ID to update
   * @param updates - Partial annotation data to update
   */
  updateAnnotation: (id: string, updates: Partial<Omit<Annotation, "id" | "timestamp">>) => void;

  /**
   * Sync status - indicates backend sync state
   */
  syncStatus: "idle" | "syncing" | "synced" | "error";

  /**
   * Last error message from sync operation
   */
  syncError: string | null;

  /**
   * Manually trigger backend sync (no-op in boomtime — localStorage-only)
   */
  syncToBackend: () => Promise<void>;

  /**
   * Clear all annotations
   */
  clearAll: () => void;

  /**
   * Auto-sync enabled state
   */
  autoSyncEnabled: boolean;

  /**
   * Toggle auto-sync on/off
   */
  setAutoSyncEnabled: (enabled: boolean) => void;
}

const AnnotationContext = createContext<AnnotationContextValue | null>(null);

const STORAGE_KEY = "catalyst-ui-annotations";
const AUTO_SYNC_KEY = "catalyst-ui-annotations-autosync";
const SYNC_INTERVAL_MS = 5000; // Sync every 5 seconds if there are annotations

/**
 * AnnotationProvider manages component annotations for boomtime's admin-only
 * devtools.
 *
 * Features:
 * - Stores annotations locally (in-memory + localStorage)
 * - CRUD operations for annotations
 * - Manual component name entry (no React Fiber introspection required)
 *
 * NOTE (boomtime fork): backend sync is disabled. `isBackendSyncEnabled()` is
 * always false here, so `syncToBackend()` and the periodic-sync effect early
 * return and never POST. Persistence is localStorage-only.
 *
 * @example
 * ```tsx
 * import { AnnotationProvider } from '@shared/features/devtools/context';
 *
 * function App() {
 *   return (
 *     <AnnotationProvider>
 *       <YourApp />
 *     </AnnotationProvider>
 *   );
 * }
 * ```
 */
export function AnnotationProvider({ children }: { children: ReactNode }) {
  // Load annotations from localStorage
  const [annotations, setAnnotations] = useState<Annotation[]>(() => {
    if (typeof window === "undefined") return [];

    try {
      const stored = localStorage.getItem(STORAGE_KEY);
      return stored ? JSON.parse(stored) : [];
    } catch (error) {
      console.error("[AnnotationProvider] Failed to load stored annotations:", error);
      return [];
    }
  });

  // Auto-sync toggle state (default: disabled)
  const [autoSyncEnabled, setAutoSyncEnabledState] = useState<boolean>(() => {
    if (typeof window === "undefined") return false;

    try {
      const stored = localStorage.getItem(AUTO_SYNC_KEY);
      return stored ? JSON.parse(stored) : false;
    } catch (error) {
      console.error("[AnnotationProvider] Failed to load auto-sync preference:", error);
      return false;
    }
  });

  // Backend sync state (retained for UI status display; never leaves "idle" in boomtime)
  const [syncStatus, setSyncStatus] = useState<"idle" | "syncing" | "synced" | "error">("idle");
  const [syncError, setSyncError] = useState<string | null>(null);
  const [, setLastSyncedAt] = useState<number>(0);

  // Wrapper to persist auto-sync preference
  const setAutoSyncEnabled = useCallback((enabled: boolean) => {
    setAutoSyncEnabledState(enabled);
    if (typeof window !== "undefined") {
      localStorage.setItem(AUTO_SYNC_KEY, JSON.stringify(enabled));
      if (isDevUtilsEnabled()) {
        console.log(`[AnnotationProvider] Auto-sync ${enabled ? "enabled" : "disabled"}`);
      }
    }
  }, []);

  // Persist annotations to localStorage
  useEffect(() => {
    if (typeof window === "undefined") return;

    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(annotations));
    } catch (error) {
      console.error("[AnnotationProvider] Failed to persist annotations:", error);
    }
  }, [annotations]);

  const getAllAnnotations = useCallback(() => annotations, [annotations]);

  const getAnnotationsByComponent = useCallback(
    (componentName: string) => {
      return annotations.filter(a => a.componentName === componentName);
    },
    [annotations]
  );

  const addAnnotation = useCallback(
    (
      annotation: Omit<Annotation, "id" | "timestamp"> &
        Partial<Pick<Annotation, "id" | "timestamp">>
    ) => {
      const newAnnotation: Annotation = {
        ...annotation,
        id: annotation.id || crypto.randomUUID(),
        timestamp: annotation.timestamp || Date.now(),
      };

      setAnnotations(prev => [...prev, newAnnotation]);

      if (isDevUtilsEnabled()) {
        console.log("[AnnotationProvider] Added annotation:", newAnnotation);
      }
    },
    []
  );

  const removeAnnotation = useCallback((id: string) => {
    setAnnotations(prev => prev.filter(a => a.id !== id));

    if (isDevUtilsEnabled()) {
      console.log(`[AnnotationProvider] Removed annotation: ${id}`);
    }
  }, []);

  const updateAnnotation = useCallback(
    (id: string, updates: Partial<Omit<Annotation, "id" | "timestamp">>) => {
      setAnnotations(prev => prev.map(a => (a.id === id ? { ...a, ...updates } : a)));

      if (isDevUtilsEnabled()) {
        console.log(`[AnnotationProvider] Updated annotation ${id}:`, updates);
      }
    },
    []
  );

  const syncToBackend = useCallback(async () => {
    // boomtime: backend sync is permanently disabled (localStorage-only). This
    // guard ensures no network request is ever issued — there is no
    // /api/annotations/sync endpoint in boomtime.
    if (!isBackendSyncEnabled()) {
      if (isDevUtilsEnabled()) {
        console.log("[AnnotationProvider] Backend sync disabled (boomtime: localStorage-only)");
      }
      return;
    }

    if (annotations.length === 0) {
      return;
    }

    setSyncStatus("syncing");
    setSyncError(null);

    try {
      const response = await fetch("/api/annotations/sync", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ annotations }),
      });

      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(`Failed to sync annotations: ${errorText}`);
      }

      await response.json();

      setSyncStatus("synced");
      setLastSyncedAt(Date.now());
    } catch (error) {
      const errorMessage = error instanceof Error ? error.message : "Sync failed";
      setSyncError(errorMessage);
      setSyncStatus("error");

      console.error("[AnnotationProvider] Sync error:", error);
    }
  }, [annotations]);

  const clearAll = useCallback(() => {
    setAnnotations([]);

    if (typeof window !== "undefined") {
      localStorage.removeItem(STORAGE_KEY);
    }

    if (isDevUtilsEnabled()) {
      console.log("[AnnotationProvider] Cleared all annotations");
    }
  }, []);

  // Periodic backend sync — disabled in boomtime (isBackendSyncEnabled() is
  // always false), so this effect always early-returns and no interval is set.
  useEffect(() => {
    if (typeof window === "undefined") return;
    if (!autoSyncEnabled) return;
    if (!isBackendSyncEnabled()) return;
    if (annotations.length === 0) return;

    const intervalId = setInterval(() => {
      syncToBackend();
    }, SYNC_INTERVAL_MS);

    return () => {
      clearInterval(intervalId);
    };
  }, [autoSyncEnabled, annotations.length, syncToBackend]);

  return (
    <AnnotationContext.Provider
      value={{
        getAllAnnotations,
        getAnnotationsByComponent,
        addAnnotation,
        removeAnnotation,
        updateAnnotation,
        syncStatus,
        syncError,
        syncToBackend,
        clearAll,
        autoSyncEnabled,
        setAutoSyncEnabled,
      }}
    >
      {children}
    </AnnotationContext.Provider>
  );
}

/**
 * Hook to access annotation context
 */
// eslint-disable-next-line react-refresh/only-export-components
export function useAnnotationContext() {
  const context = useContext(AnnotationContext);
  if (!context) {
    throw new Error("useAnnotationContext must be used within AnnotationProvider");
  }
  return context;
}
