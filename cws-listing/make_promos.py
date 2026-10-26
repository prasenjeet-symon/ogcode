#!/usr/bin/env python3
"""Render CWS promo tiles for ogweb in the ogcode sidebar palette."""
from PIL import Image, ImageDraw, ImageFont

ORANGE = (244, 81, 30)        # --accent family
ORANGE_SOFT = (255, 122, 61)
CREAM = (247, 239, 230)
MUTED = (176, 165, 155)
DARK = (23, 21, 19)           # warm near-black
PANEL = (31, 28, 26)         # panel surface
PANEL_LINE = (60, 54, 49)
GREEN = (76, 175, 110)

BOLD = "/System/Library/Fonts/HelveticaNeue.ttc"   # index 1 = Bold, 0 = Regular, 7 = Light


def font(size, bold=True):
    try:
        return ImageFont.truetype(BOLD, size, index=1 if bold else 0)
    except Exception:
        pass
    try:
        return ImageFont.truetype("/System/Library/Fonts/Supplemental/Arial Bold.ttf", size)
    except Exception:
        return ImageFont.load_default()


def light_font(size):
    try:
        return ImageFont.truetype(BOLD, size, index=7)
    except Exception:
        pass
    try:
        return ImageFont.truetype("/System/Library/Fonts/Supplemental/Arial.ttf", size)
    except Exception:
        return ImageFont.load_default()


def vertical_gradient(w, h, top, bottom):
    img = Image.new("RGB", (w, h), top)
    d = ImageDraw.Draw(img)
    for y in range(h):
        t = y / max(1, h - 1)
        d.line([(0, y), (w, y)], fill=tuple(int(top[i] + (bottom[i] - top[i]) * t) for i in range(3)))
    return img


def corner_glow(img, cx, cy, radius, inner, outer):
    """Radial-ish glow composited by per-line blending."""
    w, h = img.size
    px = img.load()
    for y in range(max(0, cy - radius), min(h, cy + radius)):
        for x in range(max(0, cx - radius), min(w, cx + radius)):
            dist = ((x - cx) ** 2 + (y - cy) ** 2) ** 0.5
            if dist < radius:
                t = (1 - dist / radius) ** 2
                r, g, b = px[x, y]
                px[x, y] = (int(r + (inner[0] - r) * t), int(g + (inner[1] - g) * t), int(b + (inner[2] - b) * t))
    return img


def draw_app_icon(d, x, y, size):
    """Rounded orange square with a white eye/aperture mark."""
    r = size * 0.24
    d.rounded_rectangle([x, y, x + size, y + size], radius=r, fill=ORANGE)
    s = size
    # aperture: white ring with a dot
    cx, cy = x + s * 0.5, y + s * 0.5
    ring = s * 0.26
    d.ellipse([cx - ring, cy - ring, cx + ring, cy + ring], outline=(255, 255, 255), width=max(2, int(s * 0.085)))
    dot = s * 0.08
    d.ellipse([cx - dot, cy - dot, cx + dot, cy + dot], fill=(255, 255, 255))


def wordmark(d, x, y, size, color=CREAM):
    f = font(size)
    d.text((x, y), "ogweb", font=f, fill=color)
    w = d.textlength("ogweb", font=f)
    # orange dot after the wordmark
    dot = size * 0.13
    bx = x + w + size * 0.08
    by = y + size * 0.80
    d.ellipse([bx, by - dot, bx + 2 * dot, by + dot], fill=ORANGE)
    return w


def draw_row(d, x, y, w, h, title, sub, checked=True):
    d.rounded_rectangle([x, y, x + w, y + h], radius=h * 0.28, fill=(44, 40, 36), outline=PANEL_LINE, width=1)
    # checkbox
    bs = h * 0.32
    cy = y + h * 0.5
    bx = x + w * 0.045
    d.rounded_rectangle([bx, cy - bs / 2, bx + bs, cy + bs / 2], radius=bs * 0.25,
                        fill=ORANGE if checked else (72, 66, 60))
    if checked:
        d.line([(bx + bs * 0.22, cy), (bx + bs * 0.42, cy + bs * 0.2)], fill=(255, 255, 255), width=max(2, int(h * 0.035)))
        d.line([(bx + bs * 0.42, cy + bs * 0.2), (bx + bs * 0.78, cy - bs * 0.24)], fill=(255, 255, 255), width=max(2, int(h * 0.035)))
    tx = bx + bs * 1.7
    cyb = cy - h * 0.16
    d.text((tx, cyb), title, font=font(h * 0.22), fill=CREAM)
    d.text((tx, cyb + h * 0.26), sub, font=light_font(h * 0.17), fill=MUTED)


def panel_mock(w_scale=1.0):
    """Return a function painting the side-panel mock of size (pw, ph) onto d at (x, y)."""
    def paint(d, x, y):
        pw, ph = int(420 * w_scale), int(360 * w_scale)
        d.rounded_rectangle([x, y, x + pw, y + ph], radius=int(18 * w_scale), fill=PANEL, outline=PANEL_LINE, width=max(1, int(1.5 * w_scale)))
        pad = int(22 * w_scale)
        # header
        draw_app_icon(d, x + pad, y + pad, int(30 * w_scale))
        d.text((x + pad + 42 * w_scale, y + pad + 4 * w_scale), "ogweb", font=font(int(20 * w_scale)), fill=CREAM)
        # status pill
        d.rounded_rectangle([x + pad, y + pad + 46 * w_scale, x + pad + 210 * w_scale, y + pad + 74 * w_scale],
                            radius=12 * w_scale, fill=(26, 24, 22))
        d.ellipse([x + pad + 10 * w_scale, y + pad + 54 * w_scale, x + pad + 20 * w_scale, y + pad + 64 * w_scale], fill=GREEN)
        d.text((x + pad + 28 * w_scale, y + pad + 50 * w_scale), "Connected  http://127.0.0.1", font=light_font(int(13 * w_scale)), fill=(200, 190, 182))
        # allow rows
        ry = y + pad + 96 * w_scale
        rh = int(58 * w_scale)
        draw_row(d, x + pad, ry, pw - 2 * pad, rh, "ogcode.in — docs", "https://ogcode.in")
        draw_row(d, x + pad, ry + rh + 12 * w_scale, pw - 2 * pad, rh, "Google News", "https://news.google.com")
        # footer caption
        fy = ry + 2 * rh + 12 * w_scale + 20 * w_scale
        d.text((x + pad, fy), "Access is checked again on every single operation.",
               font=light_font(int(13 * w_scale)), fill=MUTED)
        return pw, ph
    return paint


def centered(d, cx, y, text, f, fill):
    d.text((cx - d.textlength(text, font=f) / 2, y), text, font=f, fill=fill)


def small_tile():
    W, H = 440, 280
    img = vertical_gradient(W, H, (30, 26, 23), DARK)
    d = ImageDraw.Draw(img)
    # subtle texture: dotted grid
    for yy in range(16, H, 22):
        for xx in range(14, W, 22):
            d.ellipse([xx, yy, xx + 1.4, yy + 1.4], fill=(38, 34, 30))
    img = corner_glow(img, W - 30, H + 40, 260, (120, 52, 24), DARK)
    d = ImageDraw.Draw(img)

    draw_app_icon(d, 32, 40, 44)
    wordmark(d, 92, 42, 34)

    f_head = font(27)
    f_sub = light_font(15.5)
    d.text((32, 110), "Drive your Chrome tabs", font=f_head, fill=CREAM)
    d.text((32, 146), "from ogcode.", font=f_head, fill=ORANGE_SOFT)
    d.text((32, 194), "Your logged-in pages, only on the tabs", font=f_sub, fill=MUTED)
    d.text((32, 216), "you explicitly allow.", font=f_sub, fill=MUTED)
    # bottom chips, laid out sequentially so widths can never collide
    chip_y = 242
    x = 32
    for label in ["Local only", "You grant each tab", "No analytics"]:
        w = d.textlength(label, font=light_font(11)) + 22
        d.rounded_rectangle([x, chip_y, x + w, chip_y + 24], radius=12, fill=(40, 36, 32), outline=PANEL_LINE)
        d.text((x + 11, chip_y + 5.5), label, font=light_font(11), fill=(205, 194, 184))
        x += w + 10
    return img


def marquee_tile():
    W, H = 1400, 560
    img = vertical_gradient(W, H, (28, 25, 22), (16, 14, 13))
    d = ImageDraw.Draw(img)
    img = corner_glow(img, W - 60, H + 80, 520, (128, 54, 24), img.getpixel((0, 0)))
    d = ImageDraw.Draw(img)
    # dotted texture
    for yy in range(24, H, 34):
        for xx in range(20, W, 34):
            d = ImageDraw.Draw(img)
            d.ellipse([xx, yy, xx + 2, yy + 2], fill=(40, 36, 32))
    d = ImageDraw.Draw(img)

    draw_app_icon(d, 90, 92, 64)
    wordmark(d, 172, 96, 50)

    f_head = font(62)
    f_sub = light_font(27)
    d.text((90, 210), "Drive your Chrome tabs", font=f_head, fill=CREAM)
    d.text((90, 286), "from ogcode.", font=f_head, fill=ORANGE_SOFT)
    d.text((90, 376), "An agent sees your pages exactly as you do — logged in,", font=f_sub, fill=MUTED)
    d.text((90, 412), "cookies included — and only on tabs you explicitly allow,", font=f_sub, fill=MUTED)
    d.text((90, 448), "checked again on every operation.", font=f_sub, fill=MUTED)

    # chips bottom-left? keep clean; draw connector dots to panel
    pw, ph = panel_mock()(d, 880, 100)

    return img


if __name__ == "__main__":
    small = small_tile()
    small.save("ogweb-promo-small-440x280.png", "PNG")
    print("small:", small.size, small.mode)
    marquee = marquee_tile()
    marquee.save("ogweb-promo-marquee-1400x560.png", "PNG")
    print("marquee:", marquee.size, marquee.mode)
    # CWS requires JPEG or 24-bit PNG WITHOUT alpha — verify
    from PIL import Image as I
    for p in ("ogweb-promo-small-440x280.png", "ogweb-promo-marquee-1400x560.png"):
        im = I.open(p)
        print(p, im.mode, im.size)