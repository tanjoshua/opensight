# Epic 00 — Validation Spike (SPK)

Week-zero, before any scaffolding: validate the existential assumption for ~$10. Phase 1 (day one).

---

## SPK-1 — Core-assumption spike: does the API name local clinics?

As the founder, I want a half-day throwaway script proving the OpenAI Responses API + `web_search` with a Singapore `user_location` names specific local clinics with citations, so that seven weeks of infrastructure isn't built on an untested assumption.

- [x] Standalone script (no repo scaffolding required): calls the Responses API with `web_search` enabled and SG `user_location` on ~10 realistic clinic-style prompts (mix of category/service/condition/location phrasings).
- [x] Success bar: responses name **specific local clinics** with usable citations, consistently enough that weekly deltas would mean something. Explicit go/no-go recorded (a note in this file or `docs/`).
- [x] Raw response payloads captured and checked into `testdata/` — these seed the replay fixtures (RUN-2) and the ANA-2 quality gate.
- [x] Script is throwaway; the real implementation lands in RUN-1.

Deps: — · Phase 1 · Ref: design 01 (D1)

### Verdict: GO

Run 2026-07-18, script `spike/spk1_clinic_spike.py`, model `gpt-5-2025-08-07` (recorded in every payload), payloads in `testdata/spk1/` (`index.json` maps prompt → file).

- 10/10 prompts (3 category, 3 service, 2 condition, 2 location) returned named, specific Singapore clinics — e.g. National Dental Centre Singapore, Specialist Dental Group, T32 (dental); Central 24-HR Clinic Group, Parkway Shenton (GP); Eu Yan Sang, Bao Zhong Tang (TCM); SNEC Laser Vision Centre, Atlas Eye (LASIK); TSD Dental Tampines, Advanced Dental Tampines East (neighbourhood-level); SW1 Clinic, The Chelsea Clinic (Orchard).
- 10/10 responses carried usable `url_citation` annotations (5–16 per response, 110 total), mostly clinic-owned domains — directly maps to the PRD citation-source feature.
- Neighbourhood-level prompts resolved to clinics in that neighbourhood with addresses, confirming SG `user_location` grounding.
- Answers mix named clinics with hedging/grouping (public vs private), so mention extraction (ANA-1) must parse prose, not lists — expected and fine.
- Cost of the 10-call pass: ~$1 (383k in / 37k out tokens + web_search tool fees), well inside the $10 budget.
