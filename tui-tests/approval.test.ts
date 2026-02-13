import { test, expect } from "@microsoft/tui-test";
import { tcxBinary, baseArgs, EXPECT_TIMEOUT } from "./helpers.js";

// --- Approval prompt visibility ---
// Uses --approval-mode unless-trusted (NOT --full-auto) so tool calls require approval.
test.describe("approval prompt", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [
        ...baseArgs,
        "--model", "gpt-4o-mini",
        "--approval-mode", "unless-trusted",
        "-m", "Create a file /tmp/tui-test-approval-show.txt containing the word: hello",
      ],
    },
    rows: 30,
    columns: 120,
  });

  test("shows approval prompt for write_file", async ({ terminal }) => {
    // The approval prompt should show the tool title
    await expect(
      terminal.getByText(/Write file/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Should show selector options
    await expect(
      terminal.getByText(/Yes, allow/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/No, deny/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Status bar should show approval state
    await expect(
      terminal.getByText(/approval/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- Approve with y shortcut ---
test.describe("approve with y", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [
        ...baseArgs,
        "--model", "gpt-4o-mini",
        "--approval-mode", "unless-trusted",
        "-m", "Create a file /tmp/tui-test-approval-y.txt containing the word: hello",
      ],
    },
    rows: 30,
    columns: 120,
  });

  test("approves tool call with y shortcut", async ({ terminal }) => {
    // Wait for approval prompt
    await expect(
      terminal.getByText(/Yes, allow/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Press y to approve
    terminal.write("y");

    // After approval, the turn should complete and return to ready state
    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- Deny with n shortcut ---
test.describe("deny with n", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [
        ...baseArgs,
        "--model", "gpt-4o-mini",
        "--approval-mode", "unless-trusted",
        "-m", "Create a file /tmp/tui-test-approval-n.txt containing the word: hello",
      ],
    },
    rows: 30,
    columns: 120,
  });

  test("denies tool call with n shortcut", async ({ terminal }) => {
    // Wait for approval prompt
    await expect(
      terminal.getByText(/Yes, allow/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Press n to deny
    terminal.write("n");

    // After denial, should return to ready state (LLM may respond about denial)
    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});
