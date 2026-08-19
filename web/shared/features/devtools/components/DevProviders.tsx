import { type ReactNode } from "react";
import { AnnotationProvider } from "../context/AnnotationContext";

interface DevProvidersProps {
  children: ReactNode;
}

/**
 * Slimmed dev-mode provider wrapper (boomtime fork).
 *
 * Upstream catalyst-ui mounts both a LocalizationProvider (i18n editing) and an
 * AnnotationProvider, gated behind a build-env flag. In boomtime the i18n half
 * is dropped entirely and the devtools are admin-gated at the toggle, so this
 * wrapper mounts ONLY the AnnotationProvider and does so unconditionally — it's
 * a cheap localStorage-backed context, safe to mount for all users. The admin
 * gate lives on <DevModeToggle/> in HeaderBar.
 */
export function DevProviders({ children }: DevProvidersProps) {
  return <AnnotationProvider>{children}</AnnotationProvider>;
}
