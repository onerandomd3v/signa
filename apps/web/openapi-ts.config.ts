import { defineConfig } from "@hey-api/openapi-ts";

export default defineConfig({
  input: "../../contracts/openapi.yaml",
  output: "lib/api/generated",
});
