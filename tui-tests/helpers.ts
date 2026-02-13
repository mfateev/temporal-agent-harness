// Shared constants for TUI E2E tests.
//
// tui-test v0.0.1-rc.5: config-based expect timeout does not propagate to
// worker processes, so always pass { timeout: EXPECT_TIMEOUT } explicitly.

export const EXPECT_TIMEOUT = 60_000;

export const tcxBinary = process.env.TCX_BINARY || "../tcx";
export const temporalHost = process.env.TEMPORAL_HOST || "localhost:18233";

/** Common CLI args shared across tests. */
export const baseArgs = ["--temporal-host", temporalHost, "--no-color"];

/** Full-auto mode with gpt-4o-mini (auto-approves all tool calls). */
export const fullAutoArgs = [...baseArgs, "--full-auto", "--model", "gpt-4o-mini"];
