## What and why
<!-- What does this change, and what problem does it solve? -->

## Changes
-

## Checklist
- [ ] **Evidence-backed:** every claim or hypothesis cites real events; no guessing, and "unresolved" where the evidence doesn't decide.
- [ ] **Tests that can fail:** new logic is tested, and I checked the test fails without my change.
- [ ] **Redaction and exec:** nothing new is stored, printed, or served unredacted; nothing executes log content or runs a command without authorization.
- [ ] **Docs:** updated where behavior changed, and README/doc examples are real output.
- [ ] `gofmt -l .` is empty, and `make check` and `make lint` pass.

## How I tested
<!-- Commands you ran and what you saw. Don't claim a test passed unless you ran it. -->
```bash
make check
```
