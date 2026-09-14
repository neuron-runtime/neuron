import { Service, System } from "@neuron/sdk";

const sayHello = Service({
  name: "hello.say",
  version: "1.0.0",
  description: "Return a friendly greeting",
})
  .executor({ name: "neuron:core:set" })
  .inputSchema<{ name: string }>()
  .outputSchema<{ name: string; message: string }>();

const sayHello2 = Service({
  name: "hello.say",
  version: "1.0.0",
  description: "Return a friendly greeting",
})
    .executor({ name: "neuron:core:set" })
    .inputSchema<{ name: string }>()
    .outputSchema<{ name: string; message: string }>();

const manifest = System({
  name: "hello",
  version: "1.0.0",
  description: "A friendly hello system",
})
  .inputSchema<{ name: string }>()
    .run(sayHello.withInput({ name: "world" }))
  .toManifest();

export default manifest;
