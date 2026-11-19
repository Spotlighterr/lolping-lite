"""Bake upstream WebP timelines to independently compressed premultiplied frames.

Build-time only: Pillow reads upstream; the Go executable needs only zlib.
"""
import pathlib
import struct
import zlib
from PIL import Image

ROOT = pathlib.Path(__file__).resolve().parents[1]
SLUGS = ['unworthy_cn', 'poro_snax', 'sad_kitten', 'cheeky_poro',
         'angry_kitty', 'gg_heart', 'bee_happy', 'bee_mad', 'facepalm']

for slug in (SLUGS if __name__ == "__main__" else []):
    src = ROOT / '.upstream/assets/emotes' / (slug + '.webp')
    im = Image.open(src)
    frames = []
    elapsed = 0
    for i in range(im.n_frames):
        im.seek(i)
        rgba = im.convert('RGBA').resize((188, 188), Image.Resampling.LANCZOS)
        raw = bytearray(rgba.tobytes())
        for j in range(0, len(raw), 4):
            for k in range(3):
                raw[j+k] = (raw[j+k] * raw[j+3] + 127) // 255
        elapsed += im.info.get('duration', 40)
        frames.append((elapsed, zlib.compress(raw, 9)))
    dst = ROOT / 'assets/emotes' / (slug + '.frames')
    with dst.open('wb') as f:
        f.write(struct.pack('<III', 188, 188, len(frames)))
        for end, data in frames:
            f.write(struct.pack('<II', end, len(data)))
            f.write(data)
    print(slug, len(frames), elapsed, dst.stat().st_size)
