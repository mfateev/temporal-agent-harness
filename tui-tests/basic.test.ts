import { test, expect } from "@microsoft/tui-test";
import { tcxBinary, fullAutoArgs, EXPECT_TIMEOUT } from "./helpers.js";

test.use({
  program: {
    file: tcxBinary,
    args: [
      ...fullAutoArgs,
      "-m", "Say exactly the word: pineapple",
    ],
  },
  rows: 30,
  columns: 120,
});

test("tcx starts session and displays LLM response", async ({ terminal }) => {
  // TUI should render and start a session
  await expect(
    terminal.getByText(/Started session/g, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });

  // LLM should respond with the word "pineapple" somewhere in the output
  await expect(
    terminal.getByText(/pineapple/gi, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });
});
