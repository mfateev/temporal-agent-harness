import { test, expect } from "@microsoft/tui-test";
import { tcxBinary, baseArgs, EXPECT_TIMEOUT } from "./helpers.js";

const resumeSessionId = process.env.RESUME_SESSION_ID || "";

// Only configure the program when we have a valid session ID.
// When RESUME_SESSION_ID is empty, --session "" would fail, so we guard
// with test.when to conditionally run the test only when a seed session
// was created by run.sh.
if (resumeSessionId) {
  test.use({
    program: {
      file: tcxBinary,
      args: [
        ...baseArgs,
        "--full-auto",
        "--model", "gpt-4o-mini",
        "--session", resumeSessionId,
      ],
    },
    rows: 30,
    columns: 120,
  });
}

test.when(!!resumeSessionId, "resumes session and shows conversation history", async ({ terminal }) => {
  // The seed session had the LLM say "persimmon" — it should appear in replayed history
  await expect(
    terminal.getByText(/persimmon/gi, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });

  // Should reach ready state after resuming
  await expect(
    terminal.getByText(/ready/g, { full: true, strict: false })
  ).toBeVisible({ timeout: EXPECT_TIMEOUT });
});
