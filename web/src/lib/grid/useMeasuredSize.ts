// useMeasuredSize — ResizeObserver hook that gives a tile its actual
// rendered size regardless of whether react-grid-layout passes "300px" or
// "100%" as the style width. Mirrors hakboard's implementation.
import { useLayoutEffect, useRef, useState } from "react";

export function useMeasuredSize<T extends HTMLElement>() {
  const [size, setSize] = useState({ width: 0, height: 0 });
  const elRef = useRef<T | null>(null);
  useLayoutEffect(() => {
    const el = elRef.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    setSize({ width: rect.width, height: rect.height });
    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (!entry) return;
      const { width, height } = entry.contentRect;
      setSize({ width, height });
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return [elRef, size] as const;
}
