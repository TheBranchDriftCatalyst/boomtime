// Public barrel for the isolated grid primitive (boom-6qg extraction target).
// This is the ONLY import surface consumers should use — anything not
// re-exported here is internal to the folder.
export { DraggableGridLayout } from "./DraggableGridLayout";
export type { DraggableGridLayoutProps } from "./DraggableGridLayout";
export { WidgetHost } from "./WidgetHost";
export { ChartToggle } from "./ChartToggle";
export type { ChartToggleProps, ChartToggleView } from "./ChartToggle";
export { useMeasuredSize } from "./useMeasuredSize";
export {
  buildDefaultLayout,
  mergeLayouts,
} from "./layout-evolution";
export { localStorageAdapter, memoryAdapter } from "./storage";
export type {
  GridLayoutItem,
  WidgetInstance,
  StorageAdapter,
} from "./types";
