import { Capability, type CapabilityDefinition } from "../../capability.js";

/**
 * The value set a capability materializes.
 *
 * The names of the set are the names of the capability's configuration and
 * params, so they are not known ahead of time.
 */
export type SetParamsType = Record<string, unknown>;

/**
 * The merged value set the capability returns: its configuration with the
 * params overlaid on it.
 */
export type SetResultType = Record<string, unknown>;

/**
 * Declares a capability that materializes a value set.
 *
 * `set` is the default runtime for a capability that declares none, so most
 * assemblies never name it. It is exported for the assembly that wants to
 * declare the runtime explicitly, or to bind to a value set by name.
 *
 * @param name the capability's logical identity in the assembly.
 */
export function set(name: string): CapabilityDefinition<SetParamsType, SetResultType> {
  return Capability(name)
    .runtime({ name: "neuron:core:set" })
    .paramsSchema<SetParamsType>()
    .resultSchema<SetResultType>();
}
