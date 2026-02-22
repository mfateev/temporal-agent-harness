import { test, expect } from "@microsoft/tui-test";
import { tcxBinary, baseArgs, selectNewSession } from "./helpers.js";

// Validates that Anthropic prompt caching is visible in the TUI after a second
// turn. The status bar renders "(N cached)" when totalCachedTokens > 0.
//
// The built-in base system prompt is ~2 700 tokens, above Sonnet 4.5's
// 1 024-token cache minimum. After turn 1 the API writes the system prompt
// to its cache; turn 2 reads from it.
//
// Uses claude-sonnet-4.5-20250929 (1 024-token cache minimum).
//
// Requires: ANTHROPIC_API_KEY set in the environment.

const anthropicModel = "claude-sonnet-4.5-20250929";

// Tight per-assertion timeout: each LLM call should return in <15s.
// Using 120s per await caused 10+ minute hangs when the model was unavailable.
const ASSERTION_TIMEOUT = 30_000;

test.use({
  program: {
    file: tcxBinary,
    args: [
      ...baseArgs,
      "--full-auto",
      "--model", anthropicModel,
    ],
  },
  rows: 30,
  columns: 140,
});

test("status bar shows cached tokens after second Anthropic turn", async ({
  terminal,
}) => {
  // Skip if ANTHROPIC_API_KEY is not available in the environment.
  test.skip(
    !process.env.ANTHROPIC_API_KEY,
    "ANTHROPIC_API_KEY not set — skipping Anthropic caching TUI test",
  );

  // Navigate past the session picker → StateInput
  await selectNewSession(terminal);

  // ── Turn 1 ────────────────────────────────────────────────────────────────
  // Wait for the TUI to reach ready state (StateInput).
  await expect(
    terminal.getByText(/ready/g, { full: true, strict: false }),
  ).toBeVisible({ timeout: ASSERTION_TIMEOUT });

  // Submit the first message. The first LLM call WRITES the system prompt to
  // Anthropic's cache (cache_creation_input_tokens > 0).
  terminal.submit("Say exactly the word: lychee");

  await expect(
    terminal.getByText(/lychee/gi, { full: true, strict: false }),
  ).toBeVisible({ timeout: ASSERTION_TIMEOUT });

  // Wait for the status bar to return to "ready" before the second turn.
  await expect(
    terminal.getByText(/ready/g, { full: true, strict: false }),
  ).toBeVisible({ timeout: ASSERTION_TIMEOUT });

  // ── Turn 2 ────────────────────────────────────────────────────────────────
  // The second call sends the same system prompt → Anthropic serves it from
  // cache (cache_read_input_tokens > 0). The TUI accumulates this into
  // totalCachedTokens and renders "(N cached)" in the status bar.
  terminal.submit("Now say exactly the word: durian");

  await expect(
    terminal.getByText(/durian/gi, { full: true, strict: false }),
  ).toBeVisible({ timeout: ASSERTION_TIMEOUT });

  await expect(
    terminal.getByText(/ready/g, { full: true, strict: false }),
  ).toBeVisible({ timeout: ASSERTION_TIMEOUT });

  // ── Cache assertion ───────────────────────────────────────────────────────
  // Status bar format after a cache hit: "claude-sonnet-... · 1,234 (567 cached) tokens · turn 3 · ready"
  await expect(
    terminal.getByText(/cached/g, { full: true, strict: false }),
  ).toBeVisible({ timeout: ASSERTION_TIMEOUT });
});
