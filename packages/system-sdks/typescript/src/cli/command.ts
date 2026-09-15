#!/usr/bin/env node
import * as process from "node:process";
import { buildCmdHandler } from "./build";

async function main(): Promise<void> {
    const [command] = process.argv.slice(2);

    switch (command) {
        case "build": {
            // Extracts the path regardless of --path=/path or --path /path
            // Defaults to process.cwd() or undefined if not provided
            const pathValue = getFlagValue("--path");
            const entryValue = getFlagValue("--entry");
            await buildCmdHandler(pathValue, entryValue);
            return;
        }
        case "version":
        case "--version":
        case "-v":
            console.log(getVersion());
            return;
        case "help":
        case "--help":
        case "-h":
            printHelp();
            return;
        case undefined:
            printHelp();
            return;
        default:
            console.error(`Unknown command: "${command}"`);
            printHelp();
            process.exit(1);
    }
}

function getFlagValue(flagName: string): string | undefined {
    const args = process.argv.slice(2);

    for (let i = 0; i < args.length; i++) {
        if (args[i]?.startsWith(`${flagName}=`)) {
            return args[i]?.split("=")[1];
        }

        if (args[i] === flagName && args[i + 1] && !args[i + 1]?.startsWith("-")) {
            return args[i + 1];
        }
    }
    return undefined;
}

function getVersion(): string {
    return "0.1.0";
}

function printHelp(): void {
    console.log(`
Neuron SDK

Usage:
  neuron-sdk <command> [options]

Commands:
  build     Compile the project into a manifest (.neuron/manifest.json)
            Options:
              --path <path>   Specify the target project directory
              --entry <file>  System entry file (defaults to index.ts)
  version   Print the SDK version
  help      Show this help message
`);
}

main().catch((error) => {
    console.error("Neuron SDK error");

    if (error instanceof Error) {
        console.error(error.message);
    } else {
        console.error(error);
    }

    process.exit(1);
});
