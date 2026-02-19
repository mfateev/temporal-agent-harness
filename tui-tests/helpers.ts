// Shared constants for TUI E2E tests.
//
// tui-test v0.0.1-rc.5: config-based expect timeout does not propagate to
// worker processes, so always pass { timeout: EXPECT_TIMEOUT } explicitly.

import { expect } from "@microsoft/tui-test";

export const EXPECT_TIMEOUT = 60_000;

export const tcxBinary = process.env.TCX_BINARY || "../tcx";
export const temporalHost = process.env.TEMPORAL_HOST || "localhost:18233";

/** Common CLI args shared across tests. */
export const baseArgs = ["--temporal-host", temporalHost, "--no-color"];

/** Full-auto mode with gpt-4o-mini (auto-approves all tool calls). */
export const fullAutoArgs = [...baseArgs, "--full-auto", "--model", "gpt-4o-mini"];

/**
 * Navigate past the session picker shown when tcx starts without -m.
 * Waits for the "New session" option to appear, then presses Enter to select it.
 */
export async function selectNewSession(terminal: any): Promise<void> {
  await expect(
    terminal.getByText(/New session/g, { full: true, strict: false }),
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  // Press Enter to select "New session" (the pre-selected first option)
  terminal.submit("");
}
