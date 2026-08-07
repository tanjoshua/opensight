# Local AI Visibility — Starter MVP PRD

## 1. Product

A platform that shows local businesses how they appear in ChatGPT recommendations.

Initial market: specialist clinics in Singapore. First design-partner customer and reference account: Roots! Advanced Endodontics (root-canal specialty clinic, Singapore) — an instance of the market, not a redefinition of it; the product stays vertical-agnostic across specialist clinics.

## 2. Starter Plan

* ChatGPT only
* 20 tracked prompts
* Weekly monitoring
* Historical results

S$50 per month, billed monthly. One plan, no seats, no contract, cancel anytime.

If a subscription ends, monitoring stops but everything already collected stays viewable. Reactivating resumes weekly monitoring against the same profile and prompts; the period without a subscription shows as a gap in the history, never as an estimate.

## 3. Onboarding

The user signs up with Google and pays before the first monitoring run. Payment,
cancellation, card changes and invoices are self-serve.

The user then enters:

* Business name
* Website

The system proposes:

* Business name and aliases
* Specialist category
* Services
* Location
* 20 relevant prompts

The user reviews and edits the setup before monitoring begins. Confirmed profile values are not overwritten automatically.

## 4. Prompt Management

Users can edit or replace their 20 prompts.

When a prompt is replaced:

* Its existing history remains available.
* The replacement starts a new historical trend.
* The interface warns the user before confirming the change.

## 5. Monitoring

Each week, the system runs the 20 prompts and stores:

* Prompt
* Run time and model
* Raw response
* Business mentions and mention order
* Citations
* Run status

Sentiment, keywords, and competitor insights are derived from the stored responses.

## 6. Features

### Visibility

Show:

* Percentage of valid responses mentioning the business
* Prompts where the business appears or is absent
* Mention order
* Weekly visibility trend

Every metric links to the underlying response.

### Sentiment and Keywords

For responses mentioning the business, show:

* Positive, neutral, negative, or mixed tone
* Recurring words and themes
* Supporting excerpts
* Changes over time

### Citation Sources

Show:

* Cited domains and pages
* Citation frequency
* Prompts associated with each source
* Whether the source mentions the business or another business

### Competitor Discovery and Comparison

Automatically identify relevant businesses appearing in responses.

Show:

* Percentage of responses mentioning each competitor
* Total mentions
* Average mention order
* Prompts where each competitor appears
* Weekly trend
* Comparison with the monitored business
* Underlying responses

Users can track, dismiss, or manually add competitors.

### Improve

Show at most three evidence-backed focus actions followed by additional active recommendations. Each action shows supporting responses or checked sources, effort, steps, and verification limits. Users can start, complete, dismiss, and restore repeatable action cycles without changing the underlying assessment standing.

Show a complete visibility checklist derived from the practice catalog, current successful assessments, freshness, and action history. Practices are checks, continuous improvement areas, or tracked signals. The checklist has filterable standing counts and no score or grade. Activity history records what happened to action cycles and when; it never claims an action caused a visibility change. Completion preserves a baseline for a future genuine measured-outcome evaluator, which is not yet shown.

## 7. Product Structure

### Brief

A concise visibility briefing led by the latest analyzed run. Its adaptive visibility
explorer can inspect any analyzed run as a dated snapshot or compare runs over time
without changing evidence elsewhere in the Brief. Question changes and absence use
each question's latest analyzed responses, while sources, themes, and competitors
summarize evidence collected to date. A snapshot compares the business with current
leading competitors when that run has competitor data; the trend uses exact observed
values and never fabricates a future point. The chart is the explorer's primary
visual object; compact, low-emphasis mode and range controls stay at its edge. The
Brief also shows newly visible, no-longer-visible, and still-absent questions;
questions without two points explicitly have no baseline.

### Questions

The 20 tracked prompts and their latest results.

### Competitors

Discovered and tracked competitors with simple visibility comparisons.

### Monitoring history

Monitoring runs and all stored ChatGPT responses with mentions and citations.

### Improve

Next actions: prioritized current work. Visibility checklist: every catalog practice and the standing OpenSight can support. Activity history: the lifecycle of repeatable action cycles.

### Settings

Business profile, prompts, and competitor configuration.

## 8. Success Criteria

A user can:

1. Sign up and subscribe without contacting anyone.
2. Enter a business name and website.
3. Review an automatically generated setup.
4. Approve 20 prompts.
5. View the first ChatGPT results.
6. Understand where the business appears, how it is described, which sources are cited, and which competitors appear instead.
7. Compare visibility with relevant competitors.
8. Compare results across weekly runs.
9. Act on a small evidence-backed action queue, inspect every catalog practice, and review lifecycle history without unsupported causal claims.

## 9. Future Plans

Higher tiers may add:

* More prompts
* Daily monitoring
* Gemini, Perplexity, and other platforms
* Platform comparisons
* More advanced competitor benchmarking
* Multi-location support
* Alerts and recommendations
