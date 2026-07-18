#!/usr/bin/env python3
"""SPK-1 throwaway spike (see docs/stories/00-validation-spike.md).

Calls the OpenAI Responses API with the web_search tool and a Singapore
user_location on ~10 clinic-style prompts, saves the raw JSON payloads to
testdata/spk1/, and prints a per-prompt summary (model ID, named clinics
eyeball text, citation URLs, token usage).

Throwaway: no retries, no abstraction. Real implementation lands in RUN-1.
Run:  python3 spike/spk1_clinic_spike.py
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT_DIR = os.path.join(ROOT, "testdata", "spk1")
API_URL = "https://api.openai.com/v1/responses"
MODEL = os.environ.get("SPK1_MODEL", "gpt-5")

# Mix of category / service / condition / location phrasings (AC: ~10).
PROMPTS = [
    ("01-category-dental", "What are the best dental clinics in Singapore?"),
    ("02-category-gp", "Can you recommend a good GP clinic in Singapore?"),
    ("03-category-tcm", "Which TCM clinics in Singapore are well regarded?"),
    ("04-service-screening", "Where can I get a full health screening package in Singapore?"),
    ("05-service-lasik", "What's the best place to get LASIK eye surgery in Singapore?"),
    ("06-service-physio", "Where should I go for sports physiotherapy in Singapore?"),
    ("07-condition-backpain", "I have persistent lower back pain. Which clinic in Singapore should I see about it?"),
    ("08-condition-eczema", "My child has bad eczema. Which paediatric skin clinic in Singapore is good?"),
    ("09-location-tampines", "Is there a good dental clinic near Tampines?"),
    ("10-location-orchard", "Recommend an aesthetic clinic around Orchard Road."),
]


def load_key():
    with open(os.path.join(ROOT, ".env")) as f:
        for line in f:
            line = line.strip()
            if line.startswith("OPENAI_API_KEY"):
                return line.split("=", 1)[1].strip().strip('"').strip("'")
    sys.exit("OPENAI_API_KEY not found in .env")


def call_api(key, prompt, tool_type):
    body = {
        "model": MODEL,
        "input": prompt,
        "tools": [
            {
                "type": tool_type,
                "user_location": {
                    "type": "approximate",
                    "country": "SG",
                    "city": "Singapore",
                    "timezone": "Asia/Singapore",
                },
            }
        ],
    }
    req = urllib.request.Request(
        API_URL,
        data=json.dumps(body).encode(),
        headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=600) as resp:
        return resp.read().decode()


def summarize(raw):
    data = json.loads(raw)
    text, urls = "", []
    for item in data.get("output", []):
        if item.get("type") != "message":
            continue
        for part in item.get("content", []):
            if part.get("type") == "output_text":
                text += part.get("text", "")
                for ann in part.get("annotations", []):
                    if ann.get("type") == "url_citation":
                        urls.append(ann.get("url", ""))
    usage = data.get("usage", {})
    return data.get("model", "?"), text, urls, usage


def main():
    key = load_key()
    os.makedirs(OUT_DIR, exist_ok=True)
    tool_type = "web_search"
    index = []
    tot_in = tot_out = 0

    for slug, prompt in PROMPTS:
        print(f"\n=== {slug}: {prompt}")
        try:
            raw = call_api(key, prompt, tool_type)
        except urllib.error.HTTPError as e:
            err = e.read().decode()
            if e.code == 400 and "web_search" in err and tool_type == "web_search":
                print("  [!] web_search tool type rejected, retrying with web_search_preview")
                tool_type = "web_search_preview"
                raw = call_api(key, prompt, tool_type)
            else:
                sys.exit(f"HTTP {e.code} on {slug}: {err}")

        path = os.path.join(OUT_DIR, f"{slug}.json")
        with open(path, "w") as f:
            f.write(raw)  # unmodified raw payload

        model, text, urls, usage = summarize(raw)
        tot_in += usage.get("input_tokens", 0)
        tot_out += usage.get("output_tokens", 0)
        index.append({"slug": slug, "prompt": prompt, "file": f"{slug}.json", "model": model})

        print(f"  model: {model} | citations: {len(urls)} | tokens in/out: "
              f"{usage.get('input_tokens')}/{usage.get('output_tokens')}")
        print("  text (first 400 chars): " + text[:400].replace("\n", " "))
        for u in urls[:8]:
            print(f"    - {u}")
        time.sleep(1)

    with open(os.path.join(OUT_DIR, "index.json"), "w") as f:
        json.dump(index, f, indent=2)

    # gpt-5 list price: $1.25/M input, $10/M output (rough guide only)
    est = tot_in / 1e6 * 1.25 + tot_out / 1e6 * 10.0
    print(f"\nTotal tokens in/out: {tot_in}/{tot_out}  (~${est:.2f} at gpt-5 rates, "
          f"excl. web_search tool fee ~$0.01/call)")


if __name__ == "__main__":
    main()
