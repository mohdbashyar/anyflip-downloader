#!/usr/bin/env python3
"""
compress_pdf.py

Stand-alone PDF compression utility using PyPDF and Pillow.
Extracts page images and re-compresses them using JPEG encoding at the chosen quality.

Usage:
    python3 scripts/compress_pdf.py <input.pdf> [output.pdf] [quality]
    python3 scripts/compress_pdf.py --help
"""

import sys
import os
import time
import argparse

try:
    import pypdf
    from PIL import Image
except ImportError:
    print("Error: Missing required Python packages.")
    print("Install them with: pip install pypdf pillow")
    sys.exit(1)


def compress_pdf(input_path, output_path=None, quality=75):
    if not os.path.isfile(input_path):
        print(f"Error: Input file '{input_path}' not found.")
        sys.exit(1)

    if output_path is None:
        base, ext = os.path.splitext(input_path)
        output_path = f"{base} (Compressed){ext}"

    print(f"Reading '{input_path}'...")
    reader = pypdf.PdfReader(input_path)
    total_pages = len(reader.pages)
    print(f"Total pages: {total_pages}")

    images = []
    start_time = time.time()

    for i, page in enumerate(reader.pages):
        if len(page.images) > 0:
            pil_img = page.images[0].image.convert("RGB")
            images.append(pil_img)
        else:
            print(f"Warning: No images on page {i + 1}")

        if (i + 1) % 20 == 0 or (i + 1) == total_pages:
            elapsed = time.time() - start_time
            print(f"Extracted {i + 1}/{total_pages} pages ({elapsed:.1f}s)...")

    if not images:
        print("Error: No images found in PDF to compress.")
        sys.exit(1)

    print(f"Saving compressed PDF with JPEG quality={quality}...")
    save_start = time.time()
    images[0].save(
        output_path,
        save_all=True,
        append_images=images[1:],
        quality=quality,
        optimize=True
    )
    print(f"Saved in {time.time() - save_start:.1f}s")

    orig_size = os.path.getsize(input_path)
    new_size = os.path.getsize(output_path)
    reduction = (1 - (new_size / orig_size)) * 100
    print(f"\nOriginal size : {orig_size / (1024*1024):.2f} MB")
    print(f"Compressed size: {new_size / (1024*1024):.2f} MB")
    print(f"Size reduction : {reduction:.1f}%")
    print(f"Saved to: {output_path}")


def main():
    parser = argparse.ArgumentParser(description="Compress image-based PDF flipbooks")
    parser.add_argument("input", nargs="?", help="Input PDF file path")
    parser.add_argument("output", nargs="?", default=None, help="Output compressed PDF file path")
    parser.add_argument("quality", nargs="?", type=int, default=75, help="JPEG compression quality (1-100, default: 75)")
    parser.add_argument("-q", "--quality-flag", dest="opt_quality", type=int, default=None, help="JPEG quality flag")

    args = parser.parse_args()

    if not args.input:
        parser.print_help()
        sys.exit(1)

    quality = args.opt_quality if args.opt_quality is not None else args.quality
    compress_pdf(args.input, args.output, quality)


if __name__ == "__main__":
    main()
