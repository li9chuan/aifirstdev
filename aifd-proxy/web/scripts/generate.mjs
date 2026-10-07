import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import { fileURLToPath } from "node:url";
import path from "node:path";

const packageDir = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const repoRoot = path.resolve(packageDir, "../..");
const lock = fs.readFileSync(path.join(repoRoot, "aifd.lock"), "utf8");
const protocVersion = /^tools\.protoc:\s*(\S+)\s*$/m.exec(lock)?.[1];
if (!protocVersion) {
  throw new Error("aifd.lock does not pin tools.protoc");
}
const platform = process.platform === "win32" ? "windows" : process.platform;
const arch = os.arch() === "x64" ? "amd64" : os.arch() === "arm64" ? "arm64" : os.arch();
const executable = process.platform === "win32" ? "protoc.exe" : "protoc";
const pinnedProtoc = path.join(
  repoRoot,
  ".aifd",
  "tools",
  `${platform}-${arch}`,
  "protoc",
  protocVersion,
  "bin",
  executable
);
if (!process.env.AIFD_PROTOC && !fs.existsSync(pinnedProtoc)) {
  const install = spawnSync(
    process.env.AIFD_GO || "go",
    ["run", "./cmd/aifd", "tools", "install", "--project", repoRoot, "--tool", "protoc"],
    { cwd: path.join(repoRoot, "aifd-tools"), stdio: "inherit" }
  );
  if (install.error) {
    throw install.error;
  }
  if (install.status !== 0) {
    throw new Error(`failed to install pinned protoc ${protocVersion}`);
  }
}
const protoc = process.env.AIFD_PROTOC || pinnedProtoc;
if (!fs.existsSync(protoc)) {
  throw new Error(`pinned protoc is missing: ${protoc}`);
}
fs.mkdirSync(path.join(packageDir, "src", "gen"), { recursive: true });
const args = [
  "--proto_path=aifd-schema/proto",
  "--es_out=aifd-proxy/web/src/gen",
  "--es_opt=target=ts,import_extension=js",
  "aifd/proxy/proxy.proto"
];

const binDir = path.join(packageDir, "node_modules", ".bin");
const result = spawnSync(protoc, args, {
  cwd: repoRoot,
  env: { ...process.env, PATH: `${binDir}${path.delimiter}${process.env.PATH || ""}` },
  stdio: "inherit"
});
if (result.error) {
  throw result.error;
}
process.exit(result.status ?? 1);
