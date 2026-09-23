import { execFileSync } from "node:child_process";

const untracked = execFileSync(
  "git",
  ["ls-files", "--others", "--exclude-standard", "--", "lib/api/generated"],
  { encoding: "utf8" },
).trim();

if (untracked) {
  console.error("Untracked generated files detected:");
  console.error(untracked);
  process.exit(1);
}
