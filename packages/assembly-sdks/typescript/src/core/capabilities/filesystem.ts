import { Capability, type CapabilityDefinition } from "../../capability.js";

export type FilesystemParamsType = {
    /** What to do with the path: "read", "list", or "stat". */
    operation: "read" | "list" | "stat";
    /** The path to read, list, or describe. A relative path is resolved against the project root. */
    path: string;
    /** Whether a listing descends into subdirectories. */
    recursive?: boolean;
    /** How the content of a read file is represented: "json" or "text". */
    format?: "json" | "text";
};

export type FilesystemEntryType = {
    /** The name of the entry within its directory. */
    name: string;
    /** The absolute path of the entry. */
    path: string;
    /** What the entry is: "file", "directory", "link", or "other". */
    type: string;
    /** The size of the entry in bytes. */
    size: number;
};

export type FilesystemResultType = {
    /** The absolute path the operation applied to. */
    path: string;
    /** The content of the file, for the read operation. */
    content?: unknown;
    /** The size of the file in bytes, for the read and stat operations. */
    size?: number;
    /** The entries of the directory, for the list operation. */
    entries?: FilesystemEntryType[];
    /** Whether the path exists, for the stat operation. */
    exists?: boolean;
    /** What the path is, for the stat operation. */
    type?: string;
    /** Present when the listing was shortened to the entry limit. */
    entries_truncated?: boolean;
};

/**
 * Declares a capability that reads files and directories.
 *
 * It is the read-only view of the machine the assembly runs on: the files the
 * assembly was written to work with, and nothing else. Writing, deleting, and
 * moving files are not part of it.
 *
 * @param name the capability's logical identity in the assembly.
 */
export function filesystem(name: string): CapabilityDefinition<FilesystemParamsType, FilesystemResultType> {
    return Capability(name)
        .runtime({ name: "neuron:core:filesystem" })
        .paramsSchema<FilesystemParamsType>()
        .resultSchema<FilesystemResultType>();
}
