import { Capability, type CapabilityDefinition } from "../../capability.js";

export type CommandParamsType = {
    /**
     * The program to run. A single executable, never a shell command line.
     *
     * The program is run without a shell, so an argument can never become a
     * command.
     */
    command: string;
    /** The program's arguments. */
    args?: string[];
    /** Environment variables added to the program's environment. */
    env?: Record<string, string>;
    /** The working directory, resolved against the allowed root. */
    cwd?: string;
    /** Text written to the program's standard input. */
    stdin?: string;
    /** How long the program may run, such as "30s". */
    timeout?: string;
    /** How standard output is represented: "json" or "text". */
    format?: "json" | "text";
    /**
     * Whether a non-zero exit fails the capability. Defaults to true. Set it
     * to false to inspect the result of a failing program instead.
     */
    failOnNonZeroExit?: boolean;
    /** The most output to retain per stream. The runtime's ceiling applies when this is absent. */
    maxOutputBytes?: number;
};

export type CommandResultType = {
    /** The program's exit code. */
    exit_code: number;
    /** The program's standard output: parsed JSON in the json format, a plain string in the text format. */
    stdout: unknown;
    /** The program's standard error. */
    stderr: string;
    /** Whether the program exited with code zero. */
    success: boolean;
    /** Whether the program was terminated by its deadline. */
    timed_out: boolean;
    /** How long the program ran, in milliseconds. */
    duration_ms: number;
    /** Present when the standard output was shortened to the output limit. */
    stdout_truncated?: boolean;
    /** Present when the standard error was shortened to the output limit. */
    stderr_truncated?: boolean;
};

/**
 * Declares a capability that executes a local program.
 *
 * The program runs with an allowlisted environment, in a working directory
 * confined to the allowed roots, and with a deadline. The whole process group
 * is stopped when the run ends, so a child the program started does not
 * outlive it.
 *
 * @param name the capability's logical identity in the assembly.
 */
export function command(name: string): CapabilityDefinition<CommandParamsType, CommandResultType> {
    return Capability(name)
        .runtime({ name: "neuron:core:command" })
        .paramsSchema<CommandParamsType>()
        .resultSchema<CommandResultType>();
}
