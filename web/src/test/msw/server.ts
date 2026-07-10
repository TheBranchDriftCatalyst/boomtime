import { setupServer } from "msw/node";
import { handlers } from "@/test/msw/handlers";

// Shared msw server; started/reset/stopped in src/test/setup.ts.
export const server = setupServer(...handlers);
