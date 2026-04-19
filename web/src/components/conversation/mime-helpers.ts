// mimeIsImage / mimeIsVideo decide whether a given attachment gets an
// inline preview. Deliberately loose on the suffix (jpg, png, webp,
// gif, heic, avif all pass) — the backend hands us the source
// connector's verbatim content_type so we don't try to normalize it
// here, just gate on the prefix.

export function mimeIsImage(mime: string | undefined): boolean {
  return typeof mime === "string" && mime.startsWith("image/");
}

export function mimeIsVideo(mime: string | undefined): boolean {
  return typeof mime === "string" && mime.startsWith("video/");
}
