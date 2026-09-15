import { createSourceContext, type SourceContext } from "./expression.js";
import { makeConnection, type Connection, type InputBindings } from "./service.js";

export type { Connection, SourceContext };

export function connect<TSource extends object, TTarget extends object>(
  define: (source: SourceContext<TSource>) => InputBindings<TTarget>
): Connection<TSource, TTarget> {
  return makeConnection<TSource, TTarget>(undefined, define(createSourceContext<TSource>()));
}
