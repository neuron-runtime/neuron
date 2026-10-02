import { Capability, Assembly } from "@neuron/sdk";

const sayHello = Capability({
  name: "hello.say",
  version: "1.0.0",
  description: "Return a friendly greeting",
})
  .runtime({ name: "neuron:core:set" })
  .paramsSchema<{ name: string }>()
  .resultSchema<{ name: string; message: string }>();

const manifest = Assembly({
  name: "hello",
  version: "1.0.0",
  description: "A friendly hello assembly",
})
  .paramsSchema<{ name: string }>()
  .withParams((data) => sayHello.withParams({ name: data.name }))
  .toManifest();

export default manifest;