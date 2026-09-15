import { ServiceDefinition, ParallelCompositionHolder, CompositionNode, SequenceNode } from "./service.js";
import type { SystemNodeManifest } from "./manifest.js";
import type { Composition } from "./service.js";

export type SystemNode =
  | ServiceDefinition<object, object>
  | CompositionNode<object, object>
  | SequenceNode<object>
  | ParallelCompositionHolder
  | SystemNodeManifest;

function toNode(value: SystemNode): Composition {
  if (value instanceof CompositionNode) return value._composition;
  if (value instanceof SequenceNode) return value._composition;
  if (value instanceof ParallelCompositionHolder) return value._composition;
  if (value instanceof ServiceDefinition) {
    return {
      kind: "service",
      serviceRef: value.ref,
      serviceDef: value,
      bindings: {},
      incomingConditions: [],
    };
  }
  if (value && typeof value === "object" && value.kind) {
    return manifestToComposition(value as SystemNodeManifest);
  }
  throw new Error("Invalid node provided to Parallel");
}

function manifestToComposition(node: SystemNodeManifest): Composition {
  switch (node.kind) {
    case "service":
      return { kind: "service", serviceRef: node.service, bindings: {}, incomingConditions: [] };
    case "sequence":
      return { kind: "sequence", steps: node.steps.map(manifestToComposition) };
    case "parallel":
      return { kind: "parallel", branches: node.branches.map(manifestToComposition) };
  }
}

/**
 * Compose multiple branches to run in parallel.
 *
 * All branches execute concurrently. The parallel node completes when
 * all branches complete.
 *
 * @example
 * ```ts
 * verify.next(
 *   Parallel(
 *     saveCustomer,
 *     sendEmail,
 *     updateAnalytics
 *   )
 * )
 * ```
 */
export function Parallel(
  ...branches: SystemNode[]
): ParallelCompositionHolder {
  return new ParallelCompositionHolder(branches.map(toNode));
}
