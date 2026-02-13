import { test, expect } from "@microsoft/tui-test";
import { tcxBinary, fullAutoArgs, EXPECT_TIMEOUT } from "./helpers.js";

// Ask the LLM to run a shell command with a canary word.
// --full-auto auto-approves the shell call.
test.use({
  program: {
    file: tcxBinary,
    args: [
      ...fullAutoArgs,
      "-m", "Run this exact shell command: echo canary7742",
    ],
  },
  rows: 30,
  columns: 120,
});

test("displays tool call with verb", async ({ terminal }) => {
  // The renderer shows "Ran" as the verb for shell tool calls
  await expect(
    terminal.getByText(/Ran/g, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });
});

test("displays tool output", async ({ terminal }) => {
  // The echo output should appear in the tool output section
  await expect(
    terminal.getByText(/canary7742/g, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });
});
