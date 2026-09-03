import crypto from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";

export function sha256Buffer(buffer) {
  return crypto.createHash("sha256").update(buffer).digest("hex");
}
export async function sha256File(filePath) {
  return sha256Buffer(await fs.readFile(filePath));
}
export function nowIso() { return new Date().toISOString(); }
export function randomId(prefix = "id") {
  return `${prefix}_${crypto.randomBytes(8).toString("hex")}`;
}
export function truncateText(text, maxChars = 120000) {
  const s = String(text ?? "");
  if (s.length <= maxChars) return { text: s, truncated: false, originalChars: s.length };
  const head = Math.floor(maxChars * 0.65), tail = maxChars - head;
  return {
    text: s.slice(0, head) + `\n\n... [TRUNCATED ${s.length-maxChars} CHARS] ...\n\n` + s.slice(-tail),
    truncated: true, originalChars: s.length
  };
}
export function jsonToolResult(value) {
  return { content: [{ type: "text", text: JSON.stringify(value, null, 2) }], structuredContent: value };
}
export function errorToolResult(error) {
  return { content: [{ type: "text", text: error instanceof Error ? error.message : String(error) }], isError: true };
}
export async function pathExists(p) { try { await fs.access(p); return true; } catch { return false; } }
export function wildcardToRegExp(pattern) {
  const escaped = pattern.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*/g, ".*").replace(/\?/g, ".");
  return new RegExp(`^${escaped}$`, "i");
}
export function isProbablyBinary(buffer) {
  const sample = buffer.subarray(0, Math.min(buffer.length, 8192));
  let suspicious = 0;
  for (const byte of sample) {
    if (byte === 0) return true;
    if (byte < 7 || (byte > 13 && byte < 32)) suspicious++;
  }
  return sample.length > 0 && suspicious / sample.length > 0.08;
}
export function normalizeExecutableName(exe) {
  return path.basename(exe).replace(/\.(exe|cmd|bat)$/i, "").toLowerCase();
}
export async function ensureDir(p) { await fs.mkdir(p, { recursive: true }); }
