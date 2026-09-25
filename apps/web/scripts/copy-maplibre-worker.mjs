import { cp, mkdir } from "node:fs/promises";
import { dirname, join } from "node:path";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const packageJson = require.resolve("maplibre-gl/package.json");
const dist = join(dirname(packageJson), "dist");
const destination = join(process.cwd(), "public", "maplibre");

await mkdir(destination, { recursive: true });
for (const file of ["maplibre-gl-worker.mjs", "maplibre-gl-shared.mjs"]) {
  await cp(join(dist, file), join(destination, file));
}
