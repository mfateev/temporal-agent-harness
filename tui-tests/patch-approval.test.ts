import { test, expect } from "@microsoft/tui-test";
import { tcxBinary, baseArgs, fullAutoArgs, EXPECT_TIMEOUT } from "./helpers.js";

// --- Patch approval: file path in title ---
test.describe("patch approval shows file path in title", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [
        ...baseArgs,
        "--model", "gpt-4o-mini",
        "--approval-mode", "unless-trusted",
        "-m", "Use apply_patch to create a new file /tmp/tui-patch-title.txt with the content 'hello patch'. Use a single apply_patch call with *** Add File.",
      ],
    },
    rows: 30,
    columns: 120,
  });

  test("shows Add( title format with file path", async ({ terminal }) => {
    // The new format should show "Add(" instead of raw "Patch" title
    await expect(
      terminal.getByText(/Add\(/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Selector options should be visible
    await expect(
      terminal.getByText(/Yes, allow/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- Patch approval: diff lines visible ---
test.describe("patch approval shows diff lines", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [
        ...baseArgs,
        "--model", "gpt-4o-mini",
        "--approval-mode", "unless-trusted",
        "-m", "Use apply_patch to create a new file /tmp/tui-patch-diff.txt with the content 'alpha'. Use a single apply_patch call with *** Add File.",
      ],
    },
    rows: 30,
    columns: 120,
  });

  test("shows + lines in the preview", async ({ terminal }) => {
    // Added lines from the patch should appear with + prefix
    await expect(
      terminal.getByText(/\+/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- Approved patch: verb with file path (full-auto) ---
test.describe("approved patch shows verb with path", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [
        ...fullAutoArgs,
        "-m", "Use apply_patch to create a new file /tmp/tui-patch-auto.txt with the content 'auto'. Use a single apply_patch call with *** Add File.",
      ],
    },
    rows: 30,
    columns: 120,
  });

  test("post-execution shows file path not just Patched", async ({ terminal }) => {
    // After auto-approval, the function call display should show the file path
    await expect(
      terminal.getByText(/tui-patch-auto/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Should reach ready state
    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- Multi-file patch: shows all file paths ---
test.describe("multi-file patch shows all file paths", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [
        ...baseArgs,
        "--model", "gpt-4o-mini",
        "--approval-mode", "unless-trusted",
        "-m", "Use apply_patch with a single patch to create /tmp/tui-multi-a.txt with 'alpha' and /tmp/tui-multi-b.txt with 'beta'. Both files in one apply_patch call using *** Add File for each.",
      ],
    },
    rows: 40,
    columns: 120,
  });

  test("shows both file names in terminal", async ({ terminal }) => {
    // Both file names should be visible somewhere in the output
    await expect(
      terminal.getByText(/tui-multi-a/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/tui-multi-b/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Selector options should be visible
    await expect(
      terminal.getByText(/Yes, allow/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});
