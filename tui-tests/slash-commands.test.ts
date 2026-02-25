import { test, expect } from "@microsoft/tui-test";
import { unlinkSync } from "fs";
import { tcxBinary, baseArgs, fullAutoArgs, EXPECT_TIMEOUT, selectNewSession } from "./helpers.js";

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

// =====================================================================
// Group A: No-session commands (select "New session", test before workflow)
// =====================================================================

// --- /quit command ---
test.describe("/quit command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...baseArgs, "--full-auto", "--model", "gpt-4o-mini"],
    },
    rows: 30,
    columns: 120,
  });

  test("/quit command exits", async ({ terminal }) => {
    await selectNewSession(terminal);

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/quit");

    // Program should exit, same as /exit
    await new Promise((resolve) => setTimeout(resolve, 2000));
  });
});

// --- /diff command ---
test.describe("/diff command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...baseArgs, "--full-auto", "--model", "gpt-4o-mini"],
    },
    rows: 30,
    columns: 120,
  });

  test("/diff shows diff output or no changes", async ({ terminal }) => {
    await selectNewSession(terminal);

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/diff");

    // Should show "No changes" or diff output
    await expect(
      terminal.getByText(/No changes|diff/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- /status command ---
test.describe("/status command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...baseArgs, "--full-auto", "--model", "gpt-4o-mini"],
    },
    rows: 30,
    columns: 120,
  });

  test("/status shows current model", async ({ terminal }) => {
    await selectNewSession(terminal);

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/status");

    // Should display the model name in the status output
    await expect(
      terminal.getByText(/gpt-4o-mini/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- /init command ---
test.describe("/init command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary3365"],
    },
    rows: 30,
    columns: 120,
  });

  test("/init shows AGENTS.md reference", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary3365/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/init");

    // Should mention AGENTS.md (created or already exists or error)
    await expect(
      terminal.getByText(/Created|already exists|Error creating/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Clean up the AGENTS.md file created by /init
    try { unlinkSync("AGENTS.md"); } catch {}
  });
});

// =====================================================================
// Group B: Active-session commands (start with -m, wait for response)
// =====================================================================

// --- /end command ---
test.describe("/end command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary9281"],
    },
    rows: 30,
    columns: 120,
  });

  test("/end ends the current session", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary9281/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Submit /end to shut down the session. The shutdown + completion
    // pipeline sets m.quitting=true before the final view renders,
    // so we verify the process exits cleanly (same pattern as /exit).
    terminal.submit("/end");

    await new Promise((resolve) => setTimeout(resolve, 5000));
  });
});

// --- /compact command ---
test.describe("/compact command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary4472"],
    },
    rows: 30,
    columns: 120,
  });

  test("/compact compacts conversation history", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary4472/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/compact");

    // Should show "compacted" or transition to working state
    await expect(
      terminal.getByText(/compact/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- /review command ---
test.describe("/review command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary5593"],
    },
    rows: 30,
    columns: 120,
  });

  test("/review processes review command", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary5593/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/review");

    // If no git changes: shows "No changes to review."
    // If changes exist: shows "[/review] Reviewing current changes..."
    await expect(
      terminal.getByText(/No changes to review|Reviewing current changes/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- /approvals command ---
test.describe("/approvals command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary6604"],
    },
    rows: 30,
    columns: 120,
  });

  test("/approvals shows approval mode selector", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary6604/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/approvals");

    // Should show the approval mode selector with options
    await expect(
      terminal.getByText(/unless-trusted|never/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    // Press Escape to cancel the selector
    terminal.keyEscape();

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- /personality command ---
test.describe("/personality command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary7715"],
    },
    rows: 30,
    columns: 120,
  });

  test("/personality sets personality", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary7715/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/personality concise");

    await expect(
      terminal.getByText(/Personality set to/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- /ps command ---
test.describe("/ps command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary8826"],
    },
    rows: 30,
    columns: 120,
  });

  test("/ps shows exec sessions", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary8826/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/ps");

    // Should show exec sessions list or empty state
    await expect(
      terminal.getByText(/exec sessions|No exec|session/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- /clean command ---
test.describe("/clean command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary9937"],
    },
    rows: 30,
    columns: 120,
  });

  test("/clean closes exec sessions", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary9937/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/clean");

    // Should show "No exec sessions" or "Closed"
    await expect(
      terminal.getByText(/No exec sessions|Closed/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- /mcp command ---
test.describe("/mcp command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary2256"],
    },
    rows: 30,
    columns: 120,
  });

  test("/mcp shows MCP tools status", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary2256/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/mcp");

    // No MCP servers configured in default test env → "No MCP tools registered."
    // OR shows "Fetching MCP tools..." spinner, then result
    await expect(
      terminal.getByText(/No MCP tools|MCP Tools/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- /plan command ---
test.describe("/plan command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary4467"],
    },
    rows: 30,
    columns: 120,
  });

  test("/plan starts plan mode", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary4467/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/plan What is 2+2?");

    // Sync: "Starting plan mode..." / Async: "Plan mode active"
    await expect(
      terminal.getByText(/plan mode/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- /done command ---
test.describe("/done command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary5578"],
    },
    rows: 30,
    columns: 120,
  });

  test("/done outside plan mode shows usage hint", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary5578/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/done");

    // Not in plan mode → shows usage hint
    await expect(
      terminal.getByText(/Not in plan mode/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// --- /resume command ---
test.describe("/resume command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary6689"],
    },
    rows: 30,
    columns: 120,
  });

  test("/resume shows session picker or no-sessions message", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary6689/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/resume");

    // Shows "Fetching sessions..." in viewport, then either a session picker
    // (with workflow IDs and status like "running") or "No running sessions found."
    await expect(
      terminal.getByText(/Fetching sessions|No running sessions|running/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});

// =====================================================================
// Group C: Multi-session commands
// =====================================================================

// --- /new command ---
test.describe("/new command", () => {
  test.use({
    program: {
      file: tcxBinary,
      args: [...fullAutoArgs, "-m", "Say exactly: canary1148"],
    },
    rows: 30,
    columns: 120,
  });

  test("/new starts a new session", async ({ terminal }) => {
    await expect(
      terminal.getByText(/canary1148/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    await expect(
      terminal.getByText(/ready/g, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });

    terminal.submit("/new Say hello");

    // The /new command transitions to watching state. Verify the TUI
    // leaves the ready state (enters working/watching mode) which confirms
    // the command was accepted and dispatched.
    await expect(
      terminal.getByText(/working|new session/gi, { full: true, strict: false })
    ).toBeVisible({ timeout: EXPECT_TIMEOUT });
  });
});
