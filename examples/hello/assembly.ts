import { Capability, Assembly, string } from "@neuronruntime/sdk";

// `neuron:core:set` returns the capability's params (merged with its config),
// so the declared result ports reflect what the runtime actually produces.
const sayHello = Capability("hello.say")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<{ name: string }>()
  .resultSchema({ name: string() });

const manifest = Assembly()
  .paramsSchema<{ name: string }>()
  .resultSchema({ name: string(), message: string() })
  .withParams((data) => sayHello.withParams({ name: data.name }))
  .result({
    name: sayHello.result.name,
    message: "Hello, Neuron!",
  })
  .toManifest();

export default manifest;
