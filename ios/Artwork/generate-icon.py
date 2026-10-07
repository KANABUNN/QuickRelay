"""Render the simple vector construction in AppIcon.svg; requires Pillow."""
from pathlib import Path
from PIL import Image, ImageDraw

SCALE = 4
image = Image.new("RGB", (1024 * SCALE, 1024 * SCALE), "#101C2C")
draw = ImageDraw.Draw(image)


def stroke(points, color, width):
    scaled = [(x * SCALE, y * SCALE) for x, y in points]
    draw.line(scaled, fill=color, width=width * SCALE, joint="curve")
    radius = width * SCALE / 2
    for x, y in scaled:
        draw.ellipse((x-radius, y-radius, x+radius, y+radius), fill=color)


draw.ellipse(tuple(v * SCALE for v in (182, 172, 798, 788)), fill="#F4F7FA")
draw.ellipse(tuple(v * SCALE for v in (254, 244, 726, 716)), fill="#101C2C")
stroke([(674, 674), (800, 800)], "#E24721", 88)
stroke([(280, 484), (376, 484), (427, 375), (502, 602), (577, 435), (622, 484), (708, 484)], "#E24721", 44)
destination = Path(__file__).resolve().parents[1] / "QuakeRelay/Resources/Assets.xcassets/AppIcon.appiconset/AppIcon.png"
image.resize((1024, 1024), Image.Resampling.LANCZOS).save(destination)
print(f"Created opaque RGB icon: {destination.name}")
