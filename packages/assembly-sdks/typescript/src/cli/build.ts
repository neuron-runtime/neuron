import { mkdir, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path"
import { discoverProject } from "./project";
import { createJiti } from "jiti";
import { AssemblyManifest } from "@/manifest";
import * as process from "node:process";

// buildCmdHandler produces .neuron/manifest.json from the assembly entry file.
// The entry is provided by the neuron CLI (build --entry) which owns the
// project configuration; when omitted it defaults to <root>/index.ts.
export async function buildCmdHandler(projectDir: string = process.cwd(), entryFile?: string): Promise<void> {
  const project = discoverProject(projectDir)
  const entryFilePath = entryFile ? resolve(project.root, entryFile) : resolve(project.root, "index.ts")

  console.log("Building...")
  console.log("Entry: ", entryFilePath)

  const manifest = await loadManifest(entryFilePath);
  if (!manifest) {
    throw new Error(`Entry file ${entryFilePath} returned no default manifest export`);
  }

  await saveManifest(project.outputFile, manifest);
  console.log("Manifest written to:", project.outputFile);
}

export async function loadManifest(filePath: string): Promise<AssemblyManifest | undefined> {
  const jiti = createJiti(import.meta.url);
  const module = await jiti.import<{ default?: AssemblyManifest }>(filePath)
  return module.default;
}

export async function saveManifest(outputPath: string, manifest: AssemblyManifest | undefined): Promise<void> {
  if (!outputPath) {
    throw new Error("output path is missing.")
  }

  await mkdir(dirname(outputPath), { recursive: true })
  await writeFile(outputPath, JSON.stringify(manifest, null, 2), "utf8")
}