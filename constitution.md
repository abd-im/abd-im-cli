# Constitution

## Core Principles

### I. Current Truth
Active documents describe only the current valid state. Archive superseded material with dates instead of retaining historical comparisons.

### II. Clear Scope
Name canonical documents for their scope. Keep them concise and decision-oriented; keep research serving multiple scopes separate from active decision records.

### III. Templates Are Starting Points
Use templates for structure, never as active content. Replace every placeholder and sample with confirmed, current information.

### IV. Assignable Work
Each work item has one outcome, explicit dependencies, and verifiable acceptance criteria.

### V. One Entry Point
Maintain one concise entrypoint to active artifacts. Active lists contain only active items; inactive items are archived with dates.

### VI. Examples Are Not Limits
Treat examples as illustrative unless explicitly declared exhaustive. Model feature families with general rules and explicit authorization.

### VII. Focused Completion Commits
After completing and verifying an independent feature or work item, create a focused commit for it. Do not commit only when the user explicitly requests otherwise or relevant worktree changes must be resolved first.

### VIII. Evidence-Based Implementation
Deliver every active task and its acceptance criteria. When requirements or integration details are uncertain, inspect relevant local code, documentation, and systems as implementation evidence before deciding; the active specification remains authoritative.

## Governance

Direct user instructions supersede this document. Record every durable user correction here in the same turn, in concise and project-agnostic form; the newest instruction wins.

### Commit Cadence

Group related, verified work into meaningful delivery milestones; do not create a separate commit for every independently completed task unless the user requests that cadence.

### Controlled External Verification

When the user explicitly supplies authorized controlled-environment credentials, endpoints, or fixtures, execute the applicable integration and release gates before treating external access as unavailable.

### Complete Requested Capabilities

Treat an initial implementation as a proof path, not the endpoint of requested capability delivery. Complete the remaining requested capabilities as independently verifiable work items with explicit authorization.

### IX. Mainline Priority
Follow-up risk-remediation and observability work must be tracked separately and must not interrupt active mainline feature delivery unless the user explicitly elevates it to a release blocker.

### X. Split Oversized Work
When a task contains multiple independently verifiable outcomes or cannot be completed as one focused delivery, split it into smaller tasks with explicit dependencies and acceptance criteria. Archive the superseded task instead of leaving it indefinitely in progress.

### XI. Task-Level History
Create commits only for a completed, verified task or an independently useful governance change. Include the task's required tests, dependency update, and current-state documentation in that commit; do not commit exploration, interim fixes, or status-only updates separately.

### XII. One Supported Path
Prefer one simple, supported path for each product capability. Do not retain alternative modes or operational variants unless a demonstrated requirement justifies their user, maintenance, and verification cost.

### XIII. Cohesive Work History
Use one issue per cohesive delivery so it remains useful as a development record. Record its scope, verification, material decisions, and completion reference; keep implementation ownership in an architecture overview when one exists. Split an issue only when its record would no longer describe one focused delivery.

### XIV. Deferred Scope
Keep explicitly deferred scope inactive until the user reactivates it. Do not implement alternatives merely because the architecture could accommodate them.

### XV. Backlog Record Scope
Do not create separate empty issue records for unstarted work. Keep each item's goal, dependencies, and acceptance criteria in the active backlog; create an issue record when implementation begins so it can preserve decisions, verification, and completion history.
