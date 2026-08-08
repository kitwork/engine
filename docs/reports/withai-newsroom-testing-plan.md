# WithAI Newsroom — Testing & Verification Plan

This document details the deterministic testing strategy for the WithAI Newsroom workflow using mock skills and process restart recovery verification.

---

## 1. Testing Strategy Overview

To ensure total determinism during runtime testing:
- **Zero External LLM Calls**: Initial tests use **Deterministic Fake Skills** (hardcoded JSON mocks) to test state machine transitions, checkpoints, and human approval without API latency or cost.
- **Process Restart Recovery Test**: The test suite actively executes a process SIGKILL simulation while a Run is in state `WaitingForApproval`, restarts the host, re-hydrates the state, and verifies that the Run resumes from the exact checkpoint.

---

## 2. Test Case Matrix

| Test ID | Test Scenario | Execution Flow | Verification Assertion |
|---|---|---|---|
| **TC-01** | Full Happy Path with Approval | Create Run -> Steps 1-9 execute -> Pause at Approval -> Approve Token -> Publish -> Complete. | Run status == `Completed`; published article exists in DB. |
| **TC-02** | Rejection at Human Checkpoint | Create Run -> Pause at Approval -> Reject Token with note. | Run status == `Cancelled`; no publication executed. |
| **TC-03** | **Process SIGKILL & Resume Test** | Create Run -> Pause at Approval -> **Kill Host Process** -> **Reboot Host Process** -> Verify state `WaitingForApproval` restored -> Approve -> Complete. | Run resumes from Checkpoint without re-running Steps 1-9. |
| **TC-04** | Deduplication Trigger | Run article with identical source hash. | Run halts early at Step 3 with status `Completed` (Duplicate skipped). |
| **TC-05** | Unauthorized Publication Attempt | Attempt to call `newsroom.publish_article` directly without approval token. | Throws `SecurityError: Publication skill requires a valid approved token`. |
