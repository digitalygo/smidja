# Agents quality baseline

When edits touch the codebase, run the tests that cover the changed files before you finish. Fix every failure and re-run until all delta tests pass.

## Delta tests

- Map every behavior change to at least one test and run those tests, plus anything upstream they exercise.
- All mapped delta tests must pass. Never weaken, skip, or delete a test to make a run green.
- Failures outside the delta do not block. Report them as out-of-scope signals instead of fixing them inside an unrelated change.
- Keep coverage between 80% and 100% on the executable lines the delta adds or modifies, and report the coverage result with the change. For non-executable changes such as configuration, documentation, or generated artifacts, mark tests and coverage N/A with a short justification.

## File length

- Treat length as a contextual maintainability control, not a numerical cap. Judge it together with cohesion and separability.
- Production code normally falls in the 500 to 700 line range. Cohesive tests may reasonably approach 1000 lines.
- Never thin tests, drop cases, or split cohesive fixtures or long inputs merely to reduce length.
- Split a file across multiple files only when the file shows materially poor navigation or maintainability and a meaningful split by behavior or responsibility exists.

## Code comments

- Never write code comments. Clear naming and structure explain the code.
- If code seems to need a comment, simplify it or split it into smaller files instead.
- Use descriptive names for variables, functions, and files in place of annotation.