import { test, expect } from "@microsoft/tui-test";
import { tcxBinary, fullAutoArgs, EXPECT_TIMEOUT, selectNewSession } from "./helpers.js";

// No -m flag: starts with the session picker (interactive mode).
test.use({
  program: {
    file: tcxBinary,
    args: fullAutoArgs,
  },
  rows: 30,
  columns: 120,
});

test("supports multi-turn conversation", async ({ terminal }) => {
  // Navigate past the session picker → StateInput
  await selectNewSession(terminal);

  // Wait for ready state (StateInput — the textarea is focused)
  await expect(
    terminal.getByText(/ready/g, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });

  // Turn 1: submit a message with a canary word
  terminal.submit("Say exactly: mango9921");

  // Wait for the LLM response containing our canary word
  await expect(
    terminal.getByText(/mango9921/gi, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });

  // Wait for ready state again (turn complete, back to input).
  // Turn count is 2 after first message (system context counts as a turn).
  await expect(
    terminal.getByText(/turn 2/g, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });

  await expect(
    terminal.getByText(/ready/g, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });

  // Turn 2: submit another message with a different canary word
  terminal.submit("Say exactly: papaya3387");

  await expect(
    terminal.getByText(/papaya3387/gi, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });

  // Verify turn counter advanced to 3
  await expect(
    terminal.getByText(/turn 3/g, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });
});
