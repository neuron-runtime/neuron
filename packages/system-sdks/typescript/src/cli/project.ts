import { resolve } from "node:path";
import * as process from "node:process";

export type Project = {
  root: string;
  entryFile: string;
  outputFile: string;
}

export function discoverProject(cwd: string = process.cwd()): Project {
  const root = resolve(cwd);

  const outputFile = resolve(root, ".neuron", "manifest.json")

  return {
    root,
    entryFile: "",
    outputFile,
  }
}