import { defineConfig } from "@microsoft/tui-test";

export default defineConfig({
  timeout: 60_000,           // Per-test timeout: 60s (a single LLM turn takes <10s)
  expect: { timeout: 30_000 }, // Per-assertion timeout: 30s
  retries: 0,
  maxFailures: 1,            // Stop the entire suite on first failure
  trace: true,
  workers: 2,                // Limit parallelism to avoid LLM rate limiting
});
