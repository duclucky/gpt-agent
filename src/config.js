import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { fileURLToPath } from "node:url";
import { ensureDir, pathExists } from "./utils.js";

const HERE = path.dirname(fileURLToPath(import.meta.url));
export const APP_ROOT = path.resolve(HERE, "..");
export const DATA_DIR = process.env.GPT_AGENT_HOME ? path.resolve(process.env.GPT_AGENT_HOME) : path.join(APP_ROOT, "data");
export const CONFIG_PATH = process.env.GPT_AGENT_CONFIG ? path.resolve(process.env.GPT_AGENT_CONFIG) : path.join(DATA_DIR, "config.json");
export const AUDIT_PATH = path.join(DATA_DIR, "audit.jsonl");
export const PROCESS_DIR = path.join(DATA_DIR, "processes");
export const ARTIFACT_DIR = path.join(DATA_DIR, "artifacts");
export const FULL_SHELL_GRANT_PATH = path.join(DATA_DIR, "full-shell.grant.json");
export const TOOLS_VENV = path.join(APP_ROOT, ".venv-tools");
export const RUNTIME_VERSION = "0.1.0";

let cached;

function expandHome(p) {
  if (typeof p !== "string") return p;
  const home = process.env.HOME || process.env.USERPROFILE || os.homedir();
  if (p === "~") return home;
  if (p.startsWith("~/") || p.startsWith("~\\")) return path.join(home, p.slice(2));
  return p;
}

function resolveSpecialCommand(command) {
  if (command !== "__GPT_AGENT_TOOLS_PYTHON__") return command;
  return process.platform === "win32"
    ? path.join(TOOLS_VENV, "Scripts", "python.exe")
    : path.join(TOOLS_VENV, "bin", "python");
}

export async function loadConfig(force = false) {
  if (cached && !force) return cached;
  await ensureDir(DATA_DIR);
  await ensureDir(PROCESS_DIR);
  await ensureDir(ARTIFACT_DIR);

  if (!(await pathExists(CONFIG_PATH))) {
    await fs.copyFile(path.join(APP_ROOT, "config.example.json"), CONFIG_PATH);
    throw new Error(`Config created at ${CONFIG_PATH}. Edit workspace root, then restart GPT Agent.`);
  }

  const configText = (await fs.readFile(CONFIG_PATH, "utf8")).replace(/^\uFEFF/, "");
  const raw = JSON.parse(configText);
  if (!Array.isArray(raw.workspaces) || raw.workspaces.length === 0) throw new Error("At least one workspace is required.");

  const ids = new Set();
  raw.workspaces = raw.workspaces.filter(w => w.enabled !== false).map(w => {
    if (!w.id || !w.root) throw new Error("Each workspace needs id and root.");
    if (ids.has(w.id)) throw new Error(`Duplicate workspace id: ${w.id}`);
    ids.add(w.id);
    return { ...w, root: path.resolve(expandHome(w.root)) };
  });

  raw.server ??= {};
  raw.server.host ??= "127.0.0.1";
  raw.server.port ??= 8765;
  raw.server.maxToolOutputChars ??= 160000;

  raw.security ??= {};
  raw.security.allowSecretFiles ??= false;
  raw.security.secretFilePatterns ??= [".env", ".env.*", "*.pem", "*.key", "id_rsa", "id_ed25519"];
  raw.security.httpAllowedHosts ??= ["127.0.0.1", "localhost", "::1"];
  raw.security.fullShellGrantMinutes ??= 30;
  raw.security.requireLinuxFilesystem ??= process.platform === "linux";
  raw.security.processSandbox ??= process.platform === "linux";
  raw.security.requireProcessSandbox ??= process.platform === "linux";
  raw.security.bwrapPath ??= "/usr/bin/bwrap";
  raw.security.safeNetworkDefault ??= false;
  raw.security.maskSecretFilesInSafeProcesses ??= true;
  raw.security.requireProjectCwdForDriveWorkspace ??= true;
  raw.security.windowsSandboxMemoryMb ??= 2048;

  raw.safeCommands ??= {};
  raw.lspServers ??= {};
  raw.debugAdapters ??= {};
  for (const adapter of Object.values(raw.debugAdapters)) {
    if (adapter?.command) adapter.command = resolveSpecialCommand(adapter.command);
  }

  cached = raw;
  return raw;
}

export function clearConfigCache() { cached = undefined; }

export function detectWSL({ platform = process.platform, env = process.env, release = os.release() } = {}) {
  if (platform !== "linux") return false;
  return Boolean(env.WSL_DISTRO_NAME || env.WSL_INTEROP || /microsoft|wsl/i.test(String(release)));
}

export function isWSL() {
  return detectWSL();
}

export function workspacePerformanceWarning(root) {
  if (!isWSL()) return null;
  if (/^\/mnt\/[a-z]\//i.test(root)) {
    return "Workspace is on a Windows-mounted filesystem (/mnt/*). Move active repositories under /home/<user>/... for faster file I/O, watchers, Git and container workloads.";
  }
  return null;
}
