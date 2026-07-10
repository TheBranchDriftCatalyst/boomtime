import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import tseslint from "typescript-eslint";

export default tseslint.config(
  { ignores: ["dist"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true },
      ],
      "@typescript-eslint/no-unused-vars": [
        "warn",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
    },
  },
  {
    // Test files + the shared harness: not subject to Fast Refresh, and use
    // vitest globals (configured via `test.globals` in vite.config.ts).
    files: [
      "src/**/*.{test,spec}.{ts,tsx}",
      "src/test/**/*.{ts,tsx}",
    ],
    languageOptions: {
      globals: { ...globals.browser, ...globals.node },
    },
    rules: {
      "react-refresh/only-export-components": "off",
    },
  },
);
