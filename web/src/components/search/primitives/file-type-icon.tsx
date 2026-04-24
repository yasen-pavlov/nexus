import { createElement } from "react";
import {
  File,
  FileArchive,
  FileCode,
  FileSpreadsheet,
  FileText,
  Image as ImageIcon,
  Music,
  Presentation,
  Video,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";

// Extensions that always map to a specific family regardless of the
// mime type we were handed. Useful when Tika mislabels a file as
// application/octet-stream (common for zip-wrapped office docs, etc.).
const EXT_FAMILIES: Record<string, LucideIcon> = {
  pdf: FileText,
  doc: FileText,
  docx: FileText,
  odt: FileText,
  rtf: FileText,
  txt: FileText,
  md: FileText,
  xls: FileSpreadsheet,
  xlsx: FileSpreadsheet,
  ods: FileSpreadsheet,
  csv: FileSpreadsheet,
  tsv: FileSpreadsheet,
  ppt: Presentation,
  pptx: Presentation,
  odp: Presentation,
  key: Presentation,
  zip: FileArchive,
  gz: FileArchive,
  tar: FileArchive,
  tgz: FileArchive,
  bz2: FileArchive,
  xz: FileArchive,
  "7z": FileArchive,
  rar: FileArchive,
  png: ImageIcon,
  jpg: ImageIcon,
  jpeg: ImageIcon,
  gif: ImageIcon,
  webp: ImageIcon,
  avif: ImageIcon,
  svg: ImageIcon,
  heic: ImageIcon,
  bmp: ImageIcon,
  tiff: ImageIcon,
  mp3: Music,
  wav: Music,
  flac: Music,
  ogg: Music,
  m4a: Music,
  aac: Music,
  mp4: Video,
  mov: Video,
  mkv: Video,
  webm: Video,
  avi: Video,
  js: FileCode,
  jsx: FileCode,
  ts: FileCode,
  tsx: FileCode,
  go: FileCode,
  py: FileCode,
  rb: FileCode,
  rs: FileCode,
  java: FileCode,
  c: FileCode,
  h: FileCode,
  cpp: FileCode,
  cs: FileCode,
  php: FileCode,
  sh: FileCode,
  yaml: FileCode,
  yml: FileCode,
  json: FileCode,
  xml: FileCode,
  toml: FileCode,
  html: FileCode,
  css: FileCode,
  sql: FileCode,
};

function iconFromExtension(ext: string): LucideIcon | undefined {
  const clean = ext.toLowerCase().replace(/^\./, "");
  return EXT_FAMILIES[clean];
}

function iconFromMime(mime: string): LucideIcon | undefined {
  const m = mime.toLowerCase();
  if (m === "application/pdf") return FileText;
  if (m.startsWith("image/")) return ImageIcon;
  if (m.startsWith("audio/")) return Music;
  if (m.startsWith("video/")) return Video;
  if (
    m === "application/zip" ||
    m === "application/gzip" ||
    m === "application/x-tar" ||
    m === "application/x-7z-compressed" ||
    m === "application/x-rar-compressed"
  ) {
    return FileArchive;
  }
  if (
    m === "application/msword" ||
    m.includes("wordprocessingml") ||
    m === "application/vnd.oasis.opendocument.text" ||
    m === "text/rtf"
  ) {
    return FileText;
  }
  if (
    m === "application/vnd.ms-excel" ||
    m.includes("spreadsheetml") ||
    m === "application/vnd.oasis.opendocument.spreadsheet" ||
    m === "text/csv"
  ) {
    return FileSpreadsheet;
  }
  if (
    m === "application/vnd.ms-powerpoint" ||
    m.includes("presentationml") ||
    m === "application/vnd.oasis.opendocument.presentation"
  ) {
    return Presentation;
  }
  if (
    m === "application/json" ||
    m === "application/xml" ||
    m === "text/html" ||
    m === "text/css" ||
    m.startsWith("text/x-") ||
    m === "application/javascript" ||
    m === "application/typescript"
  ) {
    return FileCode;
  }
  if (m === "text/plain") return FileText;
  return undefined;
}

interface Props {
  mime?: string | null;
  extension?: string | null;
  className?: string;
  strokeWidth?: number;
}

// FileTypeIcon picks a lucide icon by mime first, then falls back to
// the filename extension. Extension disambiguates application/octet-stream
// — a common Tika miscast for office files.
export function FileTypeIcon({
  mime,
  extension,
  className,
  strokeWidth = 1.75,
}: Readonly<Props>) {
  const mimeLower = (mime ?? "").trim().toLowerCase();
  const fromExt = extension ? iconFromExtension(extension) : undefined;
  const fromMime = mimeLower ? iconFromMime(mimeLower) : undefined;

  // Generic octet-stream → prefer extension if we have one.
  const preferExt =
    mimeLower === "application/octet-stream" || mimeLower === "";
  const Icon = (preferExt ? fromExt ?? fromMime : fromMime ?? fromExt) ?? File;

  // Dispatched via createElement because the component identity is
  // computed at runtime (mime/extension → LucideIcon), which the
  // react-hooks static-components rule can't see through when used in
  // JSX. Functionally identical to <Icon ... />.
  return createElement(Icon, {
    className: cn(className),
    strokeWidth,
    "aria-hidden": true,
  });
}
