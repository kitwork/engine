# WithAI Newsroom — Skill Manifest Register

This document registers the 8 executable skills utilized by the WithAI Newsroom Agent.

---

## Skill Manifest Directory

| Skill Name | Description | Side-Effect Class | Requires Human Approval? | Timeout |
|---|---|---|---|---|
| `newsroom.fetch_source` | Downloads RSS feed or source HTML text. | `ReadOnly` | No | 5,000ms |
| `newsroom.deduplicate` | Computes source SHA-256 hash and checks existing SQLite entries. | `ReadOnly` | No | 2,000ms |
| `newsroom.extract_claims` | Extracts key factual assertions from article text. | `ReadOnly` | No | 10,000ms |
| `newsroom.fact_check` | Queries official primary source DB to verify claims. | `ReadOnly` | No | 10,000ms |
| `newsroom.draft_article` | Generates Vietnamese article draft HTML. | `ReversibleWrite` | No | 15,000ms |
| `newsroom.draft_x_post` | Generates 280-character X post summary. | `ReversibleWrite` | No | 5,000ms |
| **`newsroom.human_checkpoint`** | **Pauses Run & issues human approval token.** | **`Publication`** | **YES (PAUSES RUN)** | ∞ (Wait) |
| `newsroom.publish_article` | Posts approved article payload to WithAI.vn CMS API. | `Publication` | No (Pre-Approved) | 10,000ms |
