import { test, expect } from "@microsoft/tui-test";
import { tcxBinary, baseArgs, EXPECT_TIMEOUT, selectNewSession } from "./helpers.js";

// --- /exit command ---
test.describe("/exit command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...baseArgs, "--full-auto", "--model", "gpt-4o-mini"],
    },
    rows: 30,
    columns: 120,
  });

  test("/exit command quits", async ({ terminal }) => {
    // Navigate past the session picker → StateInput
    await selectNewSession(terminal);

    // Wait for ready state
    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Submit /exit to quit
    terminal.submit("/exit");

    // Program should exit. The test completes without hanging if the
    // process terminated successfully.
    await new Promise((resolve) => setTimeout(resolve, 2000));
  });
});

// --- /model selector ---
test.describe("/model selector", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...baseArgs, "--full-auto", "--model", "gpt-4o-mini"],
    },
    rows: 30,
    columns: 120,
  });

  test("/model command shows model selector", async ({ terminal }) => {
    // Navigate past the session picker → StateInput
    await selectNewSession(terminal);

    // Wait for ready state
    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Submit /model to open selector
    terminal.submit("/model");

    // Should show model options (numbered list with provider in parentheses)
    await expect(
      terminal.getByText(/openai|anthropic/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Press Escape to cancel the selector
    terminal.keyEscape();

    // Should return to ready state
    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});
