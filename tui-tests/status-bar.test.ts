import { test, expect } from "@microsoft/tui-test";
import { tcxBinary, fullAutoArgs, EXPECT_TIMEOUT } from "./helpers.js";

// Send a simple prompt so we get a full turn (status bar updates after response).
test.use({
  program: {
    file: tcxBinary,
    args: [
      ...fullAutoArgs,
      "-m", "Say exactly: kumquat",
    ],
  },
  rows: 30,
  columns: 120,
});

test("status bar shows model name", async ({ terminal }) => {
  // Wait for the LLM response first so the full layout (including status bar)
  // is rendered. The status bar shows the model name from the --model flag.
  await expect(
    terminal.getByText(/kumquat/gi, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });

  await expect(
    terminal.getByText(/gpt-4o-mini/g, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });
});

test("status bar shows token count", async ({ terminal }) => {
  await expect(
    terminal.getByText(/kumquat/gi, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });

  // Status bar format: "N tokens" or "N,NNN tokens"
  await expect(
    terminal.getByText(/tokens/g, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });
});

test("status bar shows turn count", async ({ terminal }) => {
  await expect(
    terminal.getByText(/kumquat/gi, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });

  // Turn count may be >1 due to system context messages counted as user turns
  await expect(
    terminal.getByText(/turn \d+/g, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });
});

test("status bar shows ready state after response", async ({ terminal }) => {
  await expect(
    terminal.getByText(/kumquat/gi, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });

  // After the turn completes, state should be "ready" (StateInput)
  await expect(
    terminal.getByText(/ready/g, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });
});
