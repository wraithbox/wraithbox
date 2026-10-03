#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.13"
# dependencies = [
#     "google-genai==2.25.0",
#     "pillow==12.3.0",
# ]
#
# [tool.uv]
# # Supply-chain cooldown for transitive dependencies, like the 7-day
# # minimum_release_age in .mise.toml. Move it forward deliberately when
# # bumping the pins above.
# exclude-newer = "2026-09-26T00:00:00Z"
# ///
"""
Generate the Wraith Box brand artwork drafts with the Google Gemini API.

The prompts are read from image-prompts.md next to this file, so that file
stays the single source of truth for the wording. This script encodes the
instructions around the prompts: which block quote belongs to which asset,
which negative prompts to append, the aspect ratio and size per asset, and
how many variations to make.

The API key is taken from GEMINI_API_KEY if set, otherwise read from
1Password with `op read` (see API_KEY_OP_REFERENCE).

Usage:
    ./generate-images.py --dry-run          # print every prompt, no API calls
    ./generate-images.py                    # generate everything missing
    ./generate-images.py logo-3             # only jobs whose name contains "logo-3"
    ./generate-images.py light              # only the light versions
    ./generate-images.py --count 8 logo     # 8 variations of each logo direction
    ./generate-images.py --force hero-dark  # regenerate, overwriting

Output goes to ./generated/<job>/ (gitignored). The images are raw
material: see "From raster to assets" in image-prompts.md for what to do
with them.
"""

import argparse
import io
import os
import re
import subprocess
import sys
import traceback
from dataclasses import dataclass
from pathlib import Path

from google import genai
from google.genai import types
from PIL import Image, ImageOps

# =============================================================================
# CONFIGURATION
# =============================================================================

HERE = Path(__file__).resolve().parent
PROMPTS_FILE = HERE / "image-prompts.md"
OUTPUT_DIR = HERE / "generated"

# Same model as the keynote image generator this script is based on.
MODEL = "gemini-3-pro-image-preview"

# Used when GEMINI_API_KEY is not set in the environment.
API_KEY_OP_REFERENCE = "op://AI/GEMINI_API_KEY/password"

# Gemini has no separate negative-prompt field, so the "avoid" list is
# appended to the prompt text, as image-prompts.md says to do.
AVOID_PREFIX = "Avoid:"


@dataclass
class Job:
    """One asset to generate: a prompt plus how to render and save it."""

    name: str
    prompt: str
    aspect_ratio: str  # passed to Gemini
    image_size: str  # Gemini size bucket: "1K", "2K" or "4K"
    output_size: tuple[int, int]  # final (width, height) after resizing
    count: int  # number of variations
    preview_sizes: tuple[int, ...] = ()  # extra small square copies to check by eye


# =============================================================================
# PARSING image-prompts.md
# =============================================================================


def parse_sections(markdown):
    """Map each heading's text to the list of block quotes in its section.

    A block quote is a run of consecutive lines starting with ">", joined
    into a single paragraph.
    """
    sections = {}
    current = None
    quote_lines = None

    def close_quote():
        nonlocal quote_lines
        if quote_lines is not None and current is not None:
            sections[current].append(" ".join(quote_lines).strip())
        quote_lines = None

    for line in markdown.splitlines():
        heading = re.match(r"^#{1,6}\s+(.*)$", line)
        if heading:
            close_quote()
            current = heading.group(1).strip()
            sections[current] = []
        elif line.startswith(">"):
            if quote_lines is None:
                quote_lines = []
            quote_lines.append(line[1:].strip())
        else:
            close_quote()
    close_quote()
    return sections


def parse_negative_additions(markdown):
    """Find "Negative prompt additions for the <asset>: `...`" paragraphs."""
    additions = {}
    pattern = r"Negative prompt additions for the (\w+):\s*`([^`]+)`"
    for match in re.finditer(pattern, markdown):
        additions[match.group(1)] = " ".join(match.group(2).split())
    return additions


def quotes_under(sections, heading_prefix, expected):
    """Return the block quotes under the one heading starting with heading_prefix.

    Fails loudly if the heading is missing, ambiguous, or does not have the
    expected number of block quotes, so an edit to image-prompts.md cannot
    silently produce the wrong prompt.
    """
    matches = [h for h in sections if h.startswith(heading_prefix)]
    if len(matches) != 1:
        sys.exit(
            f"Error: expected one heading starting with {heading_prefix!r} "
            f"in {PROMPTS_FILE.name}, found {matches}"
        )
    quotes = sections[matches[0]]
    if len(quotes) != expected:
        sys.exit(
            f"Error: expected {expected} block quote(s) under {matches[0]!r}, "
            f"found {len(quotes)}"
        )
    return quotes


def with_avoid(prompt, *avoid_lists):
    avoid = ", ".join(a.rstrip(".") for a in avoid_lists if a)
    return f"{prompt}\n\n{AVOID_PREFIX} {avoid}."


def build_jobs(markdown, logo_count, other_count):
    sections = parse_sections(markdown)
    additions = parse_negative_additions(markdown)
    for asset in ("icon", "hero"):
        if asset not in additions:
            sys.exit(f"Error: no 'Negative prompt additions for the {asset}' found")

    (general_negative,) = quotes_under(sections, "General negative prompt", 1)

    # Every asset section in image-prompts.md has two prompts: dark, then light.
    # (job name, heading prefix, negative additions, render settings)
    logo = dict(
        aspect_ratio="1:1", image_size="1K", output_size=(1024, 1024), count=logo_count
    )
    icon = dict(
        aspect_ratio="1:1",
        image_size="1K",
        output_size=(512, 512),
        count=other_count,
        preview_sizes=(32, 16),
    )
    hero = dict(
        aspect_ratio="4:3", image_size="2K", output_size=(1600, 1200), count=other_count
    )
    assets = [
        # (a) Logo concepts: square 1:1 1024x1024, 4 to 8 variations each.
        ("logo-1-sealed-cube", "Direction 1:", "", logo),
        ("logo-2-ghost-tile", "Direction 2:", "", logo),
        ("logo-3-bell-jar", "Direction 3:", "", logo),
        ("logo-4-monogram", "Direction 4:", "", logo),
        # (b) Icon / favicon: 512x512 1:1, then shrink to 16 and 32 px to check.
        ("icon", "(b) Icon", additions["icon"], icon),
        # (c) Hero: 4:3 at 1600x1200.
        ("hero", "(c) Hero image", additions["hero"], hero),
    ]

    jobs = []
    for name, heading, extra_negative, render in assets:
        dark, light = quotes_under(sections, heading, 2)
        for variant, prompt in (("dark", dark), ("light", light)):
            jobs.append(
                Job(
                    name=f"{name}-{variant}",
                    prompt=with_avoid(prompt, general_negative, extra_negative),
                    **render,
                )
            )

    return jobs


# =============================================================================
# GENERATION
# =============================================================================


def request_image(client, job):
    """Ask Gemini for one image. Returns a PIL image, or None."""
    response = client.models.generate_content(
        model=MODEL,
        contents=job.prompt,
        config=types.GenerateContentConfig(
            response_modalities=["TEXT", "IMAGE"],
            image_config=types.ImageConfig(
                aspect_ratio=job.aspect_ratio, image_size=job.image_size
            ),
        ),
    )

    for part in response.parts or []:
        if part.text:
            print(f"    Model note: {part.text[:200]}")
        if part.inline_data and part.inline_data.data:
            image = Image.open(io.BytesIO(part.inline_data.data))
            image.load()
            return image
    return None


def save_outputs(image, job, raw_path, final_path):
    """Save the raw image, the resized final image, and any size previews."""
    image.save(raw_path)
    print(f"    raw   {raw_path.relative_to(HERE)} {image.size[0]}x{image.size[1]}")

    final = image.convert("RGB")
    if final.size != job.output_size:
        # Crop to the exact aspect ratio (Gemini sizes are close but not
        # always exact), then downscale.
        final = ImageOps.fit(final, job.output_size, Image.Resampling.LANCZOS)
    final.save(final_path, optimize=True)
    print(f"    final {final_path.relative_to(HERE)} {final.size[0]}x{final.size[1]}")

    for px in job.preview_sizes:
        preview_path = final_path.with_name(f"{final_path.stem}-{px}px.png")
        final.resize((px, px), Image.Resampling.LANCZOS).save(preview_path)
        print(f"    check {preview_path.relative_to(HERE)}")


def run_job(client, job, dry_run, force):
    """Generate all variations of one job. Returns (succeeded, failed) names."""
    print(f"\n{'=' * 60}")
    print(
        f"{job.name}: {job.count} x {job.aspect_ratio} -> "
        f"{job.output_size[0]}x{job.output_size[1]}"
    )
    print(f"{'=' * 60}")

    if dry_run:
        print(job.prompt)
        return [], []

    job_dir = OUTPUT_DIR / job.name
    job_dir.mkdir(parents=True, exist_ok=True)
    (job_dir / "prompt.txt").write_text(job.prompt + "\n", encoding="utf-8")

    succeeded, failed = [], []
    for i in range(1, job.count + 1):
        variation = f"{job.name}-{i:02d}"
        final_path = job_dir / f"{variation}.png"
        raw_path = job_dir / f"{variation}-raw.png"

        if final_path.exists() and not force:
            print(f"  {variation}: exists, skipping (use --force to redo)")
            succeeded.append(variation)
            continue

        print(f"  {variation}: generating with {MODEL}...")
        try:
            image = request_image(client, job)
            if image is None:
                print("    ✗ No image returned")
                failed.append(variation)
                continue
            save_outputs(image, job, raw_path, final_path)
            succeeded.append(variation)
        except Exception as e:
            print(f"    ✗ Error: {e}")
            traceback.print_exc()
            failed.append(variation)

    return succeeded, failed


def get_api_key():
    """GEMINI_API_KEY from the environment, else from 1Password via `op`."""
    api_key = os.getenv("GEMINI_API_KEY")
    if api_key:
        print("API key: from GEMINI_API_KEY")
        return api_key

    print(f"API key: reading {API_KEY_OP_REFERENCE} with the 1Password CLI...")
    try:
        result = subprocess.run(
            ["op", "read", API_KEY_OP_REFERENCE],
            capture_output=True,
            text=True,
            check=True,
        )
    except FileNotFoundError:
        sys.exit(
            "Error: GEMINI_API_KEY not set and the 1Password CLI (op) is not installed."
        )
    except subprocess.CalledProcessError as e:
        sys.exit(f"Error: `op read {API_KEY_OP_REFERENCE}` failed: {e.stderr.strip()}")
    return result.stdout.strip()


# =============================================================================
# MAIN
# =============================================================================


def main():
    parser = argparse.ArgumentParser(
        description="Generate Wraith Box logo, icon and hero drafts with Gemini"
    )
    parser.add_argument(
        "--dry-run", action="store_true", help="Print prompts without calling the API"
    )
    parser.add_argument(
        "--force", action="store_true", help="Overwrite existing images"
    )
    parser.add_argument(
        "--count",
        type=int,
        help="Variations per job (default: 4 per logo job, 2 otherwise)",
    )
    parser.add_argument(
        "filter",
        nargs="?",
        help="Only run jobs whose name contains this (e.g. 'logo', 'icon-dark')",
    )
    args = parser.parse_args()

    markdown = PROMPTS_FILE.read_text(encoding="utf-8")
    jobs = build_jobs(
        markdown,
        logo_count=args.count or 4,
        other_count=args.count or 2,
    )
    if args.filter:
        jobs = [j for j in jobs if args.filter in j.name]

    total = sum(j.count for j in jobs)
    print(f"Model:   {MODEL}")
    print(f"Prompts: {PROMPTS_FILE}")
    print(f"Output:  {OUTPUT_DIR}/")
    print(f"Jobs:    {', '.join(j.name for j in jobs) or '(none)'}")
    print(f"Images:  {total}{' (dry run)' if args.dry_run else ''}")

    if not jobs:
        sys.exit("No jobs match the filter.")

    client = None
    if not args.dry_run:
        api_key = get_api_key()
        client = genai.Client(api_key=api_key)

    all_failed = []
    succeeded_count = 0
    for job in jobs:
        succeeded, failed = run_job(client, job, args.dry_run, args.force)
        succeeded_count += len(succeeded)
        all_failed.extend(failed)

    if args.dry_run:
        return

    print(f"\n{'=' * 60}")
    print(f"✓ Done:   {succeeded_count}")
    print(f"✗ Failed: {len(all_failed)}")
    for name in all_failed:
        print(f"  - {name}")
    print(
        "\nReminder: regenerate anything with invented lettering rather than "
        "retouching it, and check the icon's -16px/-32px previews."
    )
    if all_failed:
        sys.exit(1)


if __name__ == "__main__":
    main()
