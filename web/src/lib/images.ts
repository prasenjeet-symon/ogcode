// Attachment preparation for user-picked images.
//
// A photo straight off a phone is 4–10 MB and often ~4000px wide, and base64
// inflates it by another third — one such image is already past the 4 MiB
// request cap the OGX gateway enforces. The bytes are also persisted in
// `part.data` and replayed on every later turn of the session, so the cost is
// paid per request and per megabyte of database, not once. Resizing at attach
// time therefore saves both bytes and tokens: vision billing follows pixel
// area, not file size, and the provider discards or tiles whatever exceeds
// its own ceiling before counting.

/**
 * Long edge cap in pixels. Each provider normalises an image before billing
 * it — Anthropic caps a standard-tier image at ~1.15 MP (~1568 visual
 * tokens), OpenAI scales the shortest side to 768px, Gemini tiles it at
 * 768px — so sending more than those ceilings buys no model-visible detail.
 * 1024px keeps a 4:3 image at 0.79 MP, below every threshold, and cuts the
 * billed tokens per image by roughly a third to a half versus 1568px. The
 * image is replayed on every request of the session, so the saving repeats.
 */
export const MAX_ATTACHMENT_LONG_EDGE = 1024;

// A file this small that already fits the pixel cap is attached verbatim:
// re-encoding can only lose quality, and it would flatten an animated GIF.
const PASSTHROUGH_MAX_BYTES = 512 * 1024;

// Mirrors renderJPEGQuality in internal/tool/pdf_render.go.
const JPEG_QUALITY = 0.8;
const WEBP_QUALITY = 0.8;

export interface PreparedImage {
  mediaType: string;
  data: string; // base64, without the data: prefix
}

/** Read a blob as bare base64, stripping the `data:<type>;base64,` prefix. */
export function readBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = reader.result as string;
      const commaIdx = result.indexOf(',');
      resolve(commaIdx >= 0 ? result.slice(commaIdx + 1) : '');
    };
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(blob);
  });
}

type DecodedImage = ImageBitmap | HTMLImageElement;

async function decode(file: File): Promise<DecodedImage> {
  if (typeof createImageBitmap === 'function') {
    try {
      // 'from-image' applies the EXIF orientation a phone photo carries, so
      // the pixels are laid out the way the user sees them.
      return await createImageBitmap(file, { imageOrientation: 'from-image' });
    } catch {
      // Fall through to <img>, which still decodes a few formats the bitmap
      // path can refuse.
    }
  }
  const url = URL.createObjectURL(file);
  try {
    const img = new Image();
    img.src = url;
    await img.decode();
    return img;
  } finally {
    URL.revokeObjectURL(url);
  }
}

function dimensions(img: DecodedImage): { width: number; height: number } {
  if (img instanceof HTMLImageElement) {
    return { width: img.naturalWidth, height: img.naturalHeight };
  }
  return { width: img.width, height: img.height };
}

function closeDecoded(img: DecodedImage) {
  if (img instanceof ImageBitmap && typeof img.close === 'function') img.close();
}

function encodeCanvas(canvas: HTMLCanvasElement, type: string, quality: number): Promise<Blob | null> {
  return new Promise((resolve) => canvas.toBlob(resolve, type, quality));
}

/**
 * Decode a picked image file, downscale it to MAX_ATTACHMENT_LONG_EDGE and
 * re-encode it small enough to attach. Returns the bytes to store on the
 * prompt; anything that cannot be decoded or re-encoded falls back to the
 * original file so the failure surfaces upstream instead of dropping silently.
 */
export async function prepareImageAttachment(file: File): Promise<PreparedImage> {
  const raw = async (): Promise<PreparedImage> => ({ mediaType: file.type, data: await readBase64(file) });

  let img: DecodedImage;
  try {
    img = await decode(file);
  } catch {
    return raw();
  }

  const { width, height } = dimensions(img);
  const longEdge = Math.max(width, height);
  if (longEdge <= 0) {
    closeDecoded(img);
    return raw();
  }

  // Small enough to attach as-is.
  if (longEdge <= MAX_ATTACHMENT_LONG_EDGE && file.size <= PASSTHROUGH_MAX_BYTES) {
    closeDecoded(img);
    return raw();
  }

  const scale = longEdge > MAX_ATTACHMENT_LONG_EDGE ? MAX_ATTACHMENT_LONG_EDGE / longEdge : 1;
  const targetWidth = Math.max(1, Math.round(width * scale));
  const targetHeight = Math.max(1, Math.round(height * scale));

  const canvas = document.createElement('canvas');
  canvas.width = targetWidth;
  canvas.height = targetHeight;
  const ctx = canvas.getContext('2d');
  if (!ctx) {
    closeDecoded(img);
    return raw();
  }
  ctx.imageSmoothingEnabled = true;
  ctx.imageSmoothingQuality = 'high';
  try {
    ctx.drawImage(img, 0, 0, targetWidth, targetHeight);
  } catch {
    closeDecoded(img);
    return raw();
  }
  closeDecoded(img);

  // JPEG for a JPEG source — matching view_image — and WebP for everything
  // else so transparency survives. A browser without a WebP encoder returns a
  // PNG from toBlob, which is alpha-correct if larger.
  const type = file.type === 'image/jpeg' ? 'image/jpeg' : 'image/webp';
  const blob = await encodeCanvas(canvas, type, type === 'image/jpeg' ? JPEG_QUALITY : WEBP_QUALITY).catch(() => null);
  if (!blob) return raw();

  // When only the byte size triggered the re-encode, a source that came out
  // smaller stays as it was.
  if (scale === 1 && blob.size >= file.size) return raw();

  return { mediaType: blob.type || type, data: await readBase64(blob) };
}
