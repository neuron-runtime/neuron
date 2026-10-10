import { Capability, Assembly } from "@neuron/sdk";

const sayHello = Capability("hello.say")
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<{ name: string }>()
  .resultSchema<{ name: string; message: string }>();

const manifest = Assembly()
  .paramsSchema<{ name: string }>()
  .withParams((data) => sayHello.withParams({ name: data.name }))
  .toManifest();

export default manifest;