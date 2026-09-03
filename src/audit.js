import fs from "node:fs/promises";
import crypto from "node:crypto";
import path from "node:path";
import os from "node:os";
import { AUDIT_PATH } from "./config.js";
import { ensureDir, nowIso, randomId } from "./utils.js";

let lastHash = null;
let lastAuditSize = null;
let auditQueue = Promise.resolve();
const RESERVED_FIELDS = new Set(["eventId", "timestamp", "previousEventHash", "eventHash"]);
const LOCK_PATH = `${AUDIT_PATH}.lock`;
const LOCK_TIMEOUT_MS = 15000;
const LOCK_STALE_MS = 120000;

async function sleep(ms) { return await new Promise(resolve => setTimeout(resolve, ms)); }

async function readLockOwner() {
  try { return JSON.parse(await fs.readFile(path.join(LOCK_PATH, "owner.json"), "utf8")); }
  catch { return null; }
}

async function staleLock() {
  try {
    const stat = await fs.stat(LOCK_PATH);
    const owner = await readLockOwner();
    if (owner?.hostname === os.hostname() && Number.isInteger(owner.pid)) {
      try { process.kill(owner.pid, 0); return false; }
      catch (error) { if (error?.code === "ESRCH") return true; return false; }
    }
    return Date.now() - stat.mtimeMs > LOCK_STALE_MS;
  } catch { return false; }
}

async function releaseFileLock(token) {
  const owner = await readLockOwner();
  if (owner?.token !== token) return false;
  await fs.rm(LOCK_PATH, { recursive:true, force:true });
  return true;
}

async function acquireFileLock() {
  await ensureDir(path.dirname(AUDIT_PATH));
  const deadline = Date.now() + LOCK_TIMEOUT_MS;
  while (true) {
    try {
      const token = crypto.randomBytes(16).toString("hex");
      await fs.mkdir(LOCK_PATH);
      try {
        await fs.writeFile(path.join(LOCK_PATH, "owner.json"), JSON.stringify({ token, pid:process.pid, hostname:os.hostname(), acquiredAt:nowIso() }), "utf8");
      } catch (error) {
        await fs.rm(LOCK_PATH, { recursive:true, force:true }).catch(() => {});
        throw error;
      }
      return async () => { await releaseFileLock(token).catch(() => false); };
    } catch (error) {
      if (error?.code !== "EEXIST") throw error;
      if (await staleLock()) { await fs.rm(LOCK_PATH, { recursive:true, force:true }).catch(() => {}); continue; }
      if (Date.now() >= deadline) throw new Error(`Timed out waiting for audit lock: ${LOCK_PATH}`);
      await sleep(20);
    }
  }
}

function withAuditLock(fn) {
  const run = auditQueue.then(async () => {
    const release = await acquireFileLock();
    try { return await fn(); } finally { await release(); }
  }, async () => {
    const release = await acquireFileLock();
    try { return await fn(); } finally { await release(); }
  });
  auditQueue = run.then(() => undefined, () => undefined);
  return run;
}

async function discoverAuditTail() {
  let handle;
  try {
    handle = await fs.open(AUDIT_PATH, "r");
    const stat = await handle.stat();
    if (lastAuditSize !== null && stat.size === lastAuditSize) return { hash:lastHash, size:stat.size };
    if (stat.size === 0) return { hash:null, size:0 };

    let end = stat.size;
    const one = Buffer.allocUnsafe(1);
    while (end > 0) {
      await handle.read(one, 0, 1, end - 1);
      if (one[0] !== 0x0a && one[0] !== 0x0d) break;
      end--;
    }
    if (end === 0) return { hash:null, size:stat.size };

    const chunks = [];
    const chunkSize = 64 * 1024;
    let position = end;
    while (position > 0) {
      const start = Math.max(0, position - chunkSize);
      const buffer = Buffer.allocUnsafe(position - start);
      const { bytesRead } = await handle.read(buffer, 0, buffer.length, start);
      const chunk = buffer.subarray(0, bytesRead);
      const newline = chunk.lastIndexOf(0x0a);
      if (newline !== -1) {
        chunks.unshift(chunk.subarray(newline + 1));
        break;
      }
      chunks.unshift(chunk);
      position = start;
    }
    const line = Buffer.concat(chunks).toString("utf8").trim();
    return { hash:line ? JSON.parse(line).eventHash ?? null : null, size:stat.size };
  } catch (error) {
    if (error?.code === "ENOENT") return { hash:null, size:0 };
    throw error;
  } finally {
    await handle?.close().catch(() => {});
  }
}

function cleanEvent(event) {
  const out = { ...(event ?? {}) };
  for (const key of RESERVED_FIELDS) delete out[key];
  return out;
}

function hashRecord(recordWithoutHash) {
  return crypto.createHash("sha256").update(JSON.stringify(recordWithoutHash)).digest("hex");
}

async function appendAuditUnlocked(event) {
  await ensureDir(path.dirname(AUDIT_PATH));
  // Re-sync only when another process changed the file size. If this process was the last writer,
  // the cached tail is already authoritative while the cross-process lock is held.
  const tail = await discoverAuditTail();
  lastHash = tail.hash;
  lastAuditSize = tail.size;
  const record = {
    ...cleanEvent(event),
    eventId: randomId("evt"),
    timestamp: nowIso(),
    previousEventHash: lastHash
  };
  record.eventHash = hashRecord(record);
  const serialized = JSON.stringify(record) + "\n";
  await fs.appendFile(AUDIT_PATH, serialized, "utf8");
  lastHash = record.eventHash;
  lastAuditSize += Buffer.byteLength(serialized);
  return record;
}

export async function audit(event) {
  return withAuditLock(() => appendAuditUnlocked(event));
}

async function readAuditUnlocked(limit = 100) {
  try {
    const lines = (await fs.readFile(AUDIT_PATH, "utf8")).trim().split(/\r?\n/).filter(Boolean);
    return lines.slice(-Math.max(1, Math.min(limit, 1000))).map(JSON.parse);
  } catch { return []; }
}

export async function readAudit(limit = 100) {
  return withAuditLock(() => readAuditUnlocked(limit));
}

function verifyRecords(records) {
  let prev = null, count = 0;
  for (const rec of records) {
    const eventHash = rec.eventHash;
    const copy = { ...rec }; delete copy.eventHash;
    if ((copy.previousEventHash ?? null) !== prev) return { ok:false, count, reason:`Broken previous hash at ${rec.eventId}` };
    const expected = hashRecord(copy);
    if (expected !== eventHash) return { ok:false, count, reason:`Hash mismatch at ${rec.eventId}` };
    prev = eventHash; count++;
  }
  return { ok:true, count, lastHash:prev };
}

async function verifyAuditUnlocked() {
  try {
    const lines = (await fs.readFile(AUDIT_PATH, "utf8")).trim().split(/\r?\n/).filter(Boolean);
    return verifyRecords(lines.map(JSON.parse));
  } catch (e) { return { ok:false, count:0, reason:e.message }; }
}

export async function verifyAudit() {
  return withAuditLock(() => verifyAuditUnlocked());
}

export async function repairAuditChain({ confirm } = {}) {
  if (confirm !== "REPAIR") throw new Error("Audit repair requires confirm='REPAIR'.");
  return withAuditLock(async () => {
    await ensureDir(path.dirname(AUDIT_PATH));
    const raw = await fs.readFile(AUDIT_PATH, "utf8").catch(() => "");
    const lines = raw.trim().split(/\r?\n/).filter(Boolean);
    const records = lines.map(JSON.parse);
    const before = verifyRecords(records);
    if (before.ok) {
      lastHash = before.lastHash ?? null;
      lastAuditSize = Buffer.byteLength(raw);
      return { repaired:false, reason:"audit chain already valid", verify:before };
    }

    const backupDir = path.join(path.dirname(AUDIT_PATH), "audit-repair-backups");
    await ensureDir(backupDir);
    const backupSha256 = crypto.createHash("sha256").update(raw).digest("hex");
    const stamp = nowIso().replace(/[:.]/g, "-");
    const backupPath = path.join(backupDir, `${stamp}-${backupSha256.slice(0,12)}.audit.jsonl`);
    await fs.writeFile(backupPath, raw, { encoding:"utf8", flag:"wx" });

    let prev = null, changedRecords = 0;
    const repaired = [];
    for (const original of records) {
      const copy = { ...original };
      delete copy.eventHash;
      const oldPrev = copy.previousEventHash ?? null;
      copy.previousEventHash = prev;
      const newHash = hashRecord(copy);
      if (oldPrev !== prev || original.eventHash !== newHash) changedRecords++;
      copy.eventHash = newHash;
      repaired.push(copy);
      prev = newHash;
    }

    const tempPath = `${AUDIT_PATH}.${randomId("repair")}.tmp`;
    const repairedText = repaired.map(record => JSON.stringify(record)).join("\n") + (repaired.length ? "\n" : "");
    await fs.writeFile(tempPath, repairedText, "utf8");
    await fs.rename(tempPath, AUDIT_PATH);
    lastHash = prev;
    lastAuditSize = Buffer.byteLength(repairedText);

    const repairEvent = await appendAuditUnlocked({
      type:"audit.chain.repair",
      originalCount:records.length,
      changedRecords,
      originalBackupPath:backupPath,
      originalBackupSha256:backupSha256,
      previousVerifyReason:before.reason
    });
    const after = await verifyAuditUnlocked();
    if (!after.ok) throw new Error(`Audit chain repair did not verify: ${after.reason}`);
    return {
      repaired:true,
      originalCount:records.length,
      changedRecords,
      backupPath,
      backupSha256,
      repairEventId:repairEvent.eventId,
      verify:after
    };
  });
}
