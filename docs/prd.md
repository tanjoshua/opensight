# Local AI Visibility — Starter MVP PRD

## 1. Product

A platform that shows local businesses how they appear in ChatGPT recommendations.

Initial market: specialist clinics in Singapore. First design-partner customer and reference tenant: Roots! Advanced Endodontics (root-canal specialty clinic, Singapore) — an instance of the market, not a redefinition of it; the product stays vertical-agnostic across specialist clinics.

## 2. Starter Plan

* ChatGPT only
* 20 tracked prompts
* Weekly monitoring
* Historical results

## 3. Onboarding

The user enters:

* Business name
* Website

The system proposes:

* Business name and aliases
* Specialist category
* Practitioners
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

## 7. Product Structure

### Overview

Visibility, weekly change, common themes, top citation sources, and leading competitors.

### Prompts

The 20 tracked prompts and their latest results.

### Competitors

Discovered and tracked competitors with simple visibility comparisons.

### Responses

All stored ChatGPT responses with mentions and citations.

### Setup

Business profile, prompts, and competitor configuration.

## 8. Success Criteria

A user can:

1. Enter a business name and website.
2. Review an automatically generated setup.
3. Approve 20 prompts.
4. View the first ChatGPT results.
5. Understand where the business appears, how it is described, which sources are cited, and which competitors appear instead.
6. Compare visibility with relevant competitors.
7. Compare results across weekly runs.

## 9. Future Plans

Higher tiers may add:

* More prompts
* Daily monitoring
* Gemini, Perplexity, and other platforms
* Platform comparisons
* More advanced competitor benchmarking
* Multi-location support
* Alerts and recommendations
